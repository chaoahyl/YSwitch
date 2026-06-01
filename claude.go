package main

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// usageCacheEntry 保存带过期时间的 UsageSnapshot 缓存条目。
type usageCacheEntry struct {
	snapshot  UsageSnapshot
	expiresAt time.Time
}

var (
	usageCache      = map[string]usageCacheEntry{}
	usageCacheMu    sync.Mutex
	usageCacheTTL   = 60 * time.Second
)

func cachedFetchClaudeUsage(configDir string) (UsageSnapshot, error) {
	accessToken, _, _ := readClaudeOAuthInfo(configDir)
	if accessToken == "" {
		return fetchClaudeUsage(configDir)
	}
	key := accessToken

	usageCacheMu.Lock()
	if entry, ok := usageCache[key]; ok && time.Now().Before(entry.expiresAt) {
		usageCacheMu.Unlock()
		return entry.snapshot, nil
	}
	usageCacheMu.Unlock()

	snapshot, err := fetchClaudeUsage(configDir)

	if err == nil {
		usageCacheMu.Lock()
		usageCache[key] = usageCacheEntry{snapshot: snapshot, expiresAt: time.Now().Add(usageCacheTTL)}
		usageCacheMu.Unlock()
	}
	return snapshot, err
}

var claudeManagedFiles = []string{
	".credentials.json",
	"auth.json",
}

// claudeGlobalConfigPaths 按优先级返回存储 oauthAccount 的 Claude Code 全局配置文件候选路径。
func claudeGlobalConfigPaths() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return []string{
		filepath.Join(home, ".claude.json"),
		filepath.Join(home, ".claude", ".config.json"),
	}
}

// readOAuthAccountFromGlobalConfig 从 Claude Code 全局配置文件中提取 oauthAccount，不修改其他字段。
func readOAuthAccountFromGlobalConfig(configPath string) (map[string]any, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, err
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	if account := mapFromMap(raw, "oauthAccount"); account != nil {
		return account, nil
	}
	return nil, nil
}

// patchOAuthAccountInGlobalConfig 仅替换 Claude Code 全局配置中的 oauthAccount 字段，
// 其余键（projects、settings 等）保持不变。文件不存在时自动创建。
func patchOAuthAccountInGlobalConfig(configPath string, oauthAccount map[string]any) error {
	var raw map[string]any
	data, err := os.ReadFile(configPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		raw = map[string]any{}
	} else {
		if jsonErr := json.Unmarshal(data, &raw); jsonErr != nil {
			return jsonErr
		}
	}
	raw["oauthAccount"] = oauthAccount
	return writeJSON(configPath, raw)
}

// readActiveOAuthAccount 从第一个包含 oauthAccount 字段的 Claude 全局配置文件中读取该字段。
func readActiveOAuthAccount() map[string]any {
	for _, p := range claudeGlobalConfigPaths() {
		if account, err := readOAuthAccountFromGlobalConfig(p); err == nil && account != nil {
			return account
		}
	}
	return nil
}

// saveProfileOAuthAccount 将 oauthAccount 快照持久化到指定的 profile 目录。
func saveProfileOAuthAccount(profileDir string, oauthAccount map[string]any) error {
	if oauthAccount == nil {
		return nil
	}
	return writeJSON(filepath.Join(profileDir, "claude-oauth-account.json"), oauthAccount)
}

// loadProfileOAuthAccount 从 profile 目录读取已持久化的 oauthAccount。
func loadProfileOAuthAccount(profileDir string) map[string]any {
	data, err := os.ReadFile(filepath.Join(profileDir, "claude-oauth-account.json"))
	if err != nil {
		return nil
	}
	var raw map[string]any
	if json.Unmarshal(data, &raw) != nil {
		return nil
	}
	return raw
}

// applyProfileOAuthAccount 将指定 profile 保存的 oauthAccount 写入 ~/.claude.json
// （若旧版 .config.json 存在也一并更新），其余配置键保持不变。
func applyProfileOAuthAccount(profileDir string) {
	oauthAccount := loadProfileOAuthAccount(profileDir)
	if oauthAccount == nil {
		return
	}
	paths := claudeGlobalConfigPaths()
	if len(paths) == 0 {
		return
	}
	// 始终写入主路径 (~/.claude.json)，不存在则创建。
	_ = patchOAuthAccountInGlobalConfig(paths[0], oauthAccount)
	// 旧版路径仅在文件已存在时才更新。
	for _, p := range paths[1:] {
		if _, err := os.Stat(p); err == nil {
			_ = patchOAuthAccountInGlobalConfig(p, oauthAccount)
		}
	}
}

func claudeConfigDir() (string, error) {
	return legacyClaudeConfigDir()
}

func claudeVaultBase() (string, error) {
	return claudeConfigBaseDir()
}

func claudeUIStatePath() (string, error) {
	claudeDir, err := claudeConfigBaseDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(claudeDir, "state.json"), nil
}

func (a *App) GetClaudeState() (AppState, error) {
	return a.buildClaudeState()
}

func (a *App) RefreshClaudeUsage() (UsageSnapshot, error) {
	configDir, err := claudeConfigDir()
	if err != nil {
		return UsageSnapshot{}, err
	}
	// 首页刷新始终使用实时凭证，避免 vault 快照中过期的 token 触发 429。
	return cachedFetchClaudeUsage(configDir)
}

func (a *App) RefreshAllClaudeUsage() (AppState, error) {
	return a.buildClaudeState()
}

func (a *App) ActivateClaudeProfile(id string) (AppState, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return AppState{}, errors.New(tr("请选择要切换的账号", "no account selected"))
	}

	state, err := a.buildClaudeState()
	if err != nil {
		return AppState{}, err
	}

	vaultBase := state.VaultDir
	profileDir := filepath.Join(vaultBase, "profiles", id)
	manifest, err := readProfile(profileDir)
	if err != nil {
		return AppState{}, err
	}

	targetProfile := manifest.Profile
	enrichProfileFromSummary(&targetProfile, summarizeClaudeAccount(inspectClaudeFiles(profileDir)))
	if profileMatchesActive(state.Active, targetProfile) {
		return AppState{}, errors.New("already active")
	}

	// 修改凭证前先关闭 VSCode，防止其在关闭时将旧 token 写回。
	launcher, stopErr := stopVSCode()

	// 将实时凭证写回当前正在退出的 profile vault，
	// 确保下次激活时使用最新 token（VSCode 在账号使用期间会自动刷新 access token）。
	if state.UIState.SelectedProfileID != "" {
		outDir := filepath.Join(vaultBase, "profiles", state.UIState.SelectedProfileID)
		if _, statErr := os.Stat(outDir); statErr == nil {
			for _, f := range state.Files {
				if f.Exists {
					_ = copyFile(f.Path, filepath.Join(outDir, f.Name))
				}
			}
			if oauthAccount := readActiveOAuthAccount(); oauthAccount != nil {
				_ = saveProfileOAuthAccount(outDir, oauthAccount)
			}
		}
	}

	backupDir := filepath.Join(vaultBase, "saved-auth")
	activePaths := make([]string, 0, len(state.Files))
	for _, f := range state.Files {
		if f.Exists {
			activePaths = append(activePaths, f.Path)
		}
	}
	if err := moveFilesAside(activePaths, backupDir); err != nil {
		return AppState{}, fmt.Errorf("%s: %w", tr("备份旧认证文件失败", "failed to back up old auth files"), err)
	}

	writeDirs := append([]string{state.CodexDir}, windowsAnthropicDirs()...)

	for _, dir := range writeDirs {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return AppState{}, err
		}
		for _, fileName := range claudeManagedFiles {
			src := filepath.Join(profileDir, fileName)
			if _, statErr := os.Stat(src); statErr == nil {
				if err := copyFile(src, filepath.Join(dir, fileName)); err != nil {
					return AppState{}, fmt.Errorf("%s: %w", fmt.Sprintf(tr("恢复 %s 失败", "failed to restore %s"), fileName), err)
				}
			}
		}
	}

	// 更新 ~/.claude.json 中的 oauthAccount，使 CLI 上下文（组织、邮箱等）与切换后的凭证一致。
	applyProfileOAuthAccount(profileDir)

	// 更新 Windows 凭据管理器，使 VSCode 重启后读取新账号。
	updateWindowsCredentialManager(profileDir)

	restartStatus := "ok"
	if stopErr != nil {
		restartStatus = stopErr.Error()
	} else if err := startVSCode(launcher); err != nil {
		restartStatus = err.Error()
	}

	_ = a.SaveClaudeUIState(id)
	next, err := a.buildClaudeState()
	if err != nil {
		return AppState{}, err
	}
	next.RestartStatus = restartStatus
	return next, nil
}


func (a *App) QuickImportClaudeAccount() (AppState, error) {
	state, err := a.buildClaudeState()
	if err != nil {
		return AppState{}, err
	}
	name := state.Active.Label
	if name == "" {
		name = "claude-account"
	}
	if at := strings.Index(name, "@"); at > 0 {
		name = name[:at]
	}
	if runes := []rune(name); len(runes) > 24 {
		name = string(runes[:24])
	}
	return a.importClaudeAccount(name)
}

func (a *App) SaveClaudeUIState(profileID string) error {
	path, err := claudeUIStatePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return writeJSON(path, UIState{
		SelectedProfileID: profileID,
		HasActivated:      profileID != "",
	})
}

func (a *App) buildClaudeState() (AppState, error) {
	configDir, err := claudeConfigDir()
	if err != nil {
		return AppState{}, err
	}
	vaultBase, err := claudeVaultBase()
	if err != nil {
		return AppState{}, err
	}

	if err := os.MkdirAll(filepath.Join(vaultBase, "profiles"), 0o700); err != nil {
		return AppState{}, err
	}

	uiState := loadClaudeUIState()
	files := inspectClaudeFiles(configDir)
	active := summarizeClaudeAccount(files)

	// 始终从实时凭证目录派生当前账号的 label 和用量，不使用 vault——
	// vault 可能属于此前选中、但凭证已被手动替换的 profile。
	usage, _ := cachedFetchClaudeUsage(configDir)
	if !strings.Contains(active.Label, "@") {
		if usage.Label != "" {
			active.Label = usage.Label
		} else if email := fetchClaudeUserLabel(configDir); email != "" {
			active.Label = email
		} else if oauthAccount := readActiveOAuthAccount(); oauthAccount != nil {
			if email := stringFromMap(oauthAccount, "emailAddress", "email", "email_address", "user_email"); email != "" {
				active.Label = email
			}
		}
	}

	profiles, err := listClaudeProfiles(filepath.Join(vaultBase, "profiles"))
	if err != nil {
		return AppState{}, err
	}

	// 自动同步：若实时账号邮箱匹配已有 profile 但 token 不同（用户通过 VSCode 重新登录），
	// 刷新该 profile 的 vault，避免其提供过期凭证。
	if active.Label != "" && strings.Contains(active.Label, "@") {
		for i := range profiles {
			p := &profiles[i]
			labelMatch := strings.EqualFold(active.Label, p.Label) || strings.EqualFold(active.Label, p.Name)
			if !labelMatch {
				continue
			}
			tokenSame := (active.AccountID != "" && active.AccountID == p.AccountID) ||
				(active.Fingerprint != "" && active.Fingerprint == p.Fingerprint)
			if tokenSame {
				continue
			}
			profDir := filepath.Join(vaultBase, "profiles", p.ID)
			for _, f := range files {
				if f.Exists {
					_ = copyFile(f.Path, filepath.Join(profDir, f.Name))
				}
			}
			if oauthAccount := readActiveOAuthAccount(); oauthAccount != nil {
				_ = saveProfileOAuthAccount(profDir, oauthAccount)
			}
			if mf, readErr := readProfile(profDir); readErr == nil {
				mf.Profile.UpdatedAt = time.Now().Format(time.RFC3339)
				mf.Profile.Fingerprint = active.Fingerprint
				mf.Profile.AccountID = active.AccountID
				if active.Label != "" {
					mf.Profile.Label = active.Label
				}
				_ = writeJSON(filepath.Join(profDir, "profile.json"), mf)
				p.AccountID = active.AccountID
				p.Fingerprint = active.Fingerprint
				p.Label = active.Label
			}
		}
	}

	// 若存储的 SelectedProfileID 与实时凭证不再匹配
	// （如用户手动删除 .credentials.json 后重新登录），清除过期选择，使 UI 反映真实状态。
	if uiState.SelectedProfileID != "" {
		matched := false
		for _, p := range profiles {
			if p.ID == uiState.SelectedProfileID && profileMatchesActive(active, p) {
				matched = true
				break
			}
		}
		if !matched {
			uiState.SelectedProfileID = ""
			_ = a.SaveClaudeUIState("")
		}
	}

	// 当前已选 profile 即活跃账号，用最新实时用量替换 vault 中的旧快照，确保 UI 数据准确。
	if uiState.SelectedProfileID != "" {
		for i := range profiles {
			if profiles[i].ID == uiState.SelectedProfileID {
				profiles[i].Usage = usage
				break
			}
		}
	}

	return AppState{
		CodexDir: configDir,
		VaultDir: vaultBase,
		Active:   active,
		Files:    files,
		Profiles: profiles,
		Usage:    usage,
		UIState:  uiState,
	}, nil
}

func inspectClaudeFiles(configDir string) []ManagedFile {
	files := make([]ManagedFile, 0, len(claudeManagedFiles))
	for _, name := range claudeManagedFiles {
		path := filepath.Join(configDir, name)
		info, err := os.Stat(path)
		item := ManagedFile{Name: name, Path: path}
		if err == nil {
			item.Exists = true
			item.Size = info.Size()
		}
		files = append(files, item)
	}
	return files
}

func summarizeClaudeAccount(files []ManagedFile) AccountSummary {
	h := sha1.New()
	updatedAt := ""
	hasCredentialFile := false
	label := ""
	details := AccountSummary{
		Plan:              "",
		Quota:             "",
		EntitlementSource: "",
	}

	for _, file := range files {
		if !file.Exists {
			continue
		}
		data, err := os.ReadFile(file.Path)
		if err != nil {
			continue
		}
		hasCredentialFile = true
		_, _ = h.Write([]byte(file.Name))
		_, _ = h.Write(data)

		var raw map[string]any
		if jsonErr := json.Unmarshal(data, &raw); jsonErr == nil {
			mergeClaudeCredentialDetails(&details, raw)
			if email := stringFromMap(raw, "email", "user_email", "account", "login"); email != "" {
				label = email
				details.Label = email
			}
			if details.Label == "" {
				if email := extractAccountLabel(data); email != "" {
					label = email
					details.Label = email
				}
			}
			if details.Label == "" {
				oauth := mapFromMap(raw, "claudeAiOauth", "claude_ai_oauth", "oauth")
				if token := stringFromMap(oauth, "accessToken", "access_token"); token != "" {
					if claims := decodeJWTClaims(token); claims != nil {
						if email := stringFromMap(claims, "email", "preferred_username", "name"); email != "" {
							label = email
							details.Label = email
						}
					}
				}
			}
			if mode := stringFromMap(raw, "auth_mode", "authMode", "type", "mode"); mode != "" {
				details.AuthMode = mode
			}
			if apiKey := stringFromMap(raw, "apiKey", "api_key", "key", "token"); apiKey != "" && details.AuthMode == "" {
				details.AuthMode = "api_key"
				if details.AccountID == "" {
					details.AccountID = apiKey[:min(12, len(apiKey))] + "..."
				}
			}
			if plan := stringFromMap(raw, "plan", "tier", "subscription"); plan != "" {
				details.Plan = plan
			}
			if details.Label == "" && details.AccountID != "" {
				label = "Claude " + shortAccountID(details.AccountID)
				details.Label = label
			}
		}

		if extracted := extractAuthDetails(data); extracted.Label != "" && details.Label == "" {
			label = extracted.Label
			mergeAccountDetails(&details, extracted)
		}

		if info, err := os.Stat(file.Path); err == nil {
			ts := info.ModTime().Format("2006-01-02 15:04:05")
			if ts > updatedAt {
				updatedAt = ts
			}
		}
	}

	sum := h.Sum(nil)
	fingerprint := ""
	if hasCredentialFile && len(sum) > 0 {
		fingerprint = strings.ToUpper(hex.EncodeToString(sum)[:12])
	}
	if label == "" && hasCredentialFile {
		fp := ""
		if len(fingerprint) >= 6 {
			fp = fingerprint[:6]
		}
		if details.AuthMode == "oauth" {
			parts := []string{"claude"}
			if fp != "" {
				parts = append(parts, fp)
			}
			if details.Plan != "" {
				parts = append(parts, details.Plan)
			}
			label = strings.Join(parts, " · ")
		} else if details.AuthMode != "" {
			parts := []string{"claude"}
			if fp != "" {
				parts = append(parts, fp)
			}
			parts = append(parts, details.AuthMode)
			label = strings.Join(parts, " · ")
		}
	}
	details.Label = label
	details.Fingerprint = fingerprint
	details.UpdatedAt = updatedAt
	return details
}

func mergeClaudeCredentialDetails(details *AccountSummary, raw map[string]any) {
	if organizationUUID := stringFromMap(raw, "organizationUuid", "organization_uuid", "organizationID", "organizationId"); organizationUUID != "" {
		details.AccountID = organizationUUID
	}

	oauth := mapFromMap(raw, "claudeAiOauth", "claude_ai_oauth", "oauth")
	if oauth == nil {
		return
	}

	if token := stringFromMap(oauth, "accessToken", "access_token", "refreshToken", "refresh_token"); token != "" && details.AuthMode == "" {
		details.AuthMode = "claude"
	}
	// 当没有组织 UUID 时，从 refresh token 派生稳定账号 ID
	// （VSCode Claude 扩展不在凭证中存储 organizationUuid）
	if details.AccountID == "" {
		if refreshToken := stringFromMap(oauth, "refreshToken", "refresh_token"); refreshToken != "" {
			details.AccountID = deriveOAuthAccountID(refreshToken)
		}
	}
	subscriptionType := stringFromMap(oauth, "subscriptionType", "subscription_type", "plan", "tier", "subscription")
	rateLimitTier := stringFromMap(oauth, "rateLimitTier", "rate_limit_tier")
	if subscriptionType != "" {
		details.Plan = subscriptionType
	} else if rateLimitTier != "" {
		details.Plan = rateLimitTier
	}
	if rateLimitTier != "" {
		details.Quota = rateLimitTier
	}
}

// deriveOAuthAccountID 从 OAuth refresh token 生成稳定的短账号标识符。
// refresh token 在整个登录会话期间保持不变，适合作为账号 ID。
func deriveOAuthAccountID(refreshToken string) string {
	h := sha1.New()
	h.Write([]byte(refreshToken))
	return "ant-" + strings.ToUpper(hex.EncodeToString(h.Sum(nil))[:12])
}

func shortAccountID(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= 12 {
		return value
	}
	return value[:8] + "..." + value[len(value)-4:]
}

func fetchClaudeUsage(configDir string) (UsageSnapshot, error) {
	accessToken, orgUUID, planType := readClaudeOAuthInfo(configDir)

	base := UsageSnapshot{
		Source:    "claude",
		PlanType:  planType,
		AccountID: orgUUID,
		UpdatedAt: time.Now().Format(time.RFC3339),
		Windows:   make([]RateLimitWindow, 0),
	}
	if accessToken == "" {
		base.Status = tr("未读取到认证信息", "no auth info")
		return base, nil
	}

	// 若 JWT 的 exp 声明已过期，则跳过网络请求。
	// vault 快照使用静态 token 会过期，此处给出明确状态，避免 API 返回误导性的 429。
	if claims := decodeJWTClaims(accessToken); claims != nil {
		if exp, ok := claims["exp"].(float64); ok && time.Now().Unix() > int64(exp) {
			msg := tr("认证已过期", "token expired")
			base.Status = msg
			return base, errors.New(msg)
		}
	}

	req, err := http.NewRequest(http.MethodGet, "https://api.anthropic.com/api/oauth/usage", nil)
	if err != nil {
		base.Status = err.Error()
		return base, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "claude-account-switcher")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		base.Status = err.Error()
		return base, err
	}
	defer resp.Body.Close()
	bodyBytes, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == 429 {
		msg := tr("账号已限流或认证失效", "rate limited or auth invalid")
		base.Status = msg
		return base, errors.New(msg)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := fmt.Sprintf(tr("接口返回 %d", "API returned %d"), resp.StatusCode)
		base.Status = msg
		return base, errors.New(msg)
	}

	return parseClaudeOAuthUsage(bodyBytes, orgUUID, planType), nil
}

type claudeOAuthUsageResp struct {
	FiveHour      *claudeUsageWindow `json:"five_hour"`
	SevenDay      *claudeUsageWindow `json:"seven_day"`
	SevenDaySonnet *claudeUsageWindow `json:"seven_day_sonnet"`
	ExtraUsage    *claudeExtraUsage  `json:"extra_usage"`
}

type claudeUsageWindow struct {
	Utilization *float64 `json:"utilization"`
	ResetsAt    string   `json:"resets_at"`
}

type claudeExtraUsage struct {
	IsEnabled    bool     `json:"is_enabled"`
	MonthlyLimit *float64 `json:"monthly_limit"`
	UsedCredits  *float64 `json:"used_credits"`
	Utilization  *float64 `json:"utilization"`
}

func parseClaudeOAuthUsage(data []byte, orgUUID, planType string) UsageSnapshot {
	snapshot := UsageSnapshot{
		Source:    "claude",
		Status:    "ok",
		PlanType:  planType,
		AccountID: orgUUID,
		UpdatedAt: time.Now().Format(time.RFC3339),
		Windows:   make([]RateLimitWindow, 0),
	}

	var raw claudeOAuthUsageResp
	if err := json.Unmarshal(data, &raw); err != nil {
		snapshot.Status = tr("解析响应失败", "failed to parse response") + ": " + err.Error()
		return snapshot
	}

	type windowDef struct {
		id   string
		name string
		mins int
		w    *claudeUsageWindow
	}
	defs := []windowDef{
		{"five_hour", "", 300, raw.FiveHour},
		{"seven_day", "", 10080, raw.SevenDay},
		{"seven_day_sonnet", "7d-Sonnet", 10080, raw.SevenDaySonnet},
	}
	for _, d := range defs {
		if d.w == nil || d.w.Utilization == nil {
			continue
		}
		snapshot.Windows = append(snapshot.Windows, RateLimitWindow{
			LimitID:            d.id,
			LimitName:          d.name,
			UsedPercent:        *d.w.Utilization,
			WindowDurationMins: d.mins,
			ResetsAt:           d.w.ResetsAt,
		})
	}

	if raw.ExtraUsage != nil && raw.ExtraUsage.IsEnabled {
		snapshot.Credits = CreditsSummary{HasCredits: true}
		if raw.ExtraUsage.UsedCredits != nil && raw.ExtraUsage.MonthlyLimit != nil && *raw.ExtraUsage.MonthlyLimit > 0 {
			snapshot.Credits.Balance = fmt.Sprintf("%.2f / %.2f", *raw.ExtraUsage.UsedCredits, *raw.ExtraUsage.MonthlyLimit)
		}
	}

	if len(snapshot.Windows) == 0 {
		snapshot.Status = tr("接口未返回用量数据", "no usage data in response")
	}
	return snapshot
}

var (
	claudeEmailCache   = map[string]string{} // accessToken → email（按 token 永久缓存）
	claudeEmailCacheMu sync.Mutex
)

// fetchClaudeUserLabel 调用 Anthropic API 获取用户邮箱地址。
// 结果按 access token 缓存，失败的查询不会阻塞后续状态加载。
func fetchClaudeUserLabel(configDir string) string {
	accessToken, _, _ := readClaudeOAuthInfo(configDir)
	if accessToken == "" {
		return ""
	}

	claudeEmailCacheMu.Lock()
	if email, ok := claudeEmailCache[accessToken]; ok {
		claudeEmailCacheMu.Unlock()
		return email
	}
	claudeEmailCacheMu.Unlock()

	// 按命中概率排序的端点列表，依据 token 的 user:profile 权限范围
	// 以及 Anthropic 已知的 URL 规律（/api/oauth/usage 已可用）
	endpoints := []string{
		"https://api.anthropic.com/api/user/profile",
		"https://api.anthropic.com/api/oauth/profile",
		"https://api.anthropic.com/api/users/me",
		"https://api.anthropic.com/api/profile",
	}
	client := &http.Client{Timeout: 6 * time.Second}
	var found string
	for _, endpoint := range endpoints {
		req, err := http.NewRequest(http.MethodGet, endpoint, nil)
		if err != nil {
			continue
		}
		req.Header.Set("Authorization", "Bearer "+accessToken)
		req.Header.Set("anthropic-version", "2023-06-01")
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "claude-account-switcher")
		resp, err := client.Do(req)
		if err != nil {
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			continue
		}
		var raw map[string]any
		if json.Unmarshal(body, &raw) != nil {
			continue
		}
		if email := stringFromMap(raw, "email", "emailAddress", "email_address", "user_email"); email != "" {
			found = email
			break
		}
		for _, key := range []string{"account", "user", "profile", "oauthAccount"} {
			if nested := mapFromMap(raw, key); nested != nil {
				if email := stringFromMap(nested, "email", "emailAddress", "email_address"); email != "" {
					found = email
					break
				}
			}
		}
		if found != "" {
			break
		}
	}

	claudeEmailCacheMu.Lock()
	claudeEmailCache[accessToken] = found
	claudeEmailCacheMu.Unlock()
	return found
}

func readClaudeOAuthInfo(configDir string) (accessToken, orgUUID, planType string) {
	var refreshToken string
	for _, fileName := range claudeManagedFiles {
		data, err := os.ReadFile(filepath.Join(configDir, fileName))
		if err != nil {
			continue
		}
		var raw map[string]any
		if json.Unmarshal(data, &raw) != nil {
			continue
		}
		if uuid := stringFromMap(raw, "organizationUuid", "organization_uuid"); uuid != "" && orgUUID == "" {
			orgUUID = uuid
		}
		oauth := mapFromMap(raw, "claudeAiOauth", "claude_ai_oauth", "oauth")
		if oauth == nil {
			continue
		}
		if token := stringFromMap(oauth, "accessToken", "access_token"); token != "" && accessToken == "" {
			accessToken = token
		}
		if rt := stringFromMap(oauth, "refreshToken", "refresh_token"); rt != "" && refreshToken == "" {
			refreshToken = rt
		}
		if st := stringFromMap(oauth, "subscriptionType", "subscription_type"); st != "" && planType == "" {
			planType = st
		}
	}
	// VSCode Claude 扩展不存储 organizationUuid；从 refresh token 派生稳定 ID
	if orgUUID == "" && refreshToken != "" {
		orgUUID = deriveOAuthAccountID(refreshToken)
	}
	return
}


func loadClaudeUIState() UIState {
	path, err := claudeUIStatePath()
	if err != nil {
		return UIState{}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return UIState{}
	}
	var s UIState
	if err := json.Unmarshal(data, &s); err != nil {
		return UIState{}
	}
	return s
}

func (a *App) importClaudeAccount(name string) (AppState, error) {
	state, err := a.buildClaudeState()
	if err != nil {
		return AppState{}, err
	}

	if profile, ok := findDuplicateProfile(state.Active, state.Profiles); ok {
		// 强匹配（token/fingerprint 相同）：该账号已保存，直接返回。
		isTokenMatch := (state.Active.AccountID != "" && state.Active.AccountID == profile.AccountID) ||
			(state.Active.Fingerprint != "" && state.Active.Fingerprint == profile.Fingerprint)
		if isTokenMatch {
			return AppState{}, fmt.Errorf("%s: %s", tr("当前账号已保存", "account already saved"), profile.Name)
		}
		// 弱匹配（仅 label/邮箱相同）：用户以相同账号重新登录，用新凭证刷新已有 profile vault。
		profDir := filepath.Join(state.VaultDir, "profiles", profile.ID)
		for _, f := range state.Files {
			if !f.Exists {
				continue
			}
			if err := copyFile(f.Path, filepath.Join(profDir, f.Name)); err != nil {
				return AppState{}, fmt.Errorf("%s: %w", fmt.Sprintf(tr("更新凭证 %s 失败", "failed to update credential %s"), f.Name), err)
			}
		}
		if oauthAccount := readActiveOAuthAccount(); oauthAccount != nil {
			_ = saveProfileOAuthAccount(profDir, oauthAccount)
		}
		if mf, readErr := readProfile(profDir); readErr == nil {
			mf.Profile.UpdatedAt = time.Now().Format(time.RFC3339)
			mf.Profile.Fingerprint = state.Active.Fingerprint
			mf.Profile.AccountID = state.Active.AccountID
			if state.Active.Label != "" {
				mf.Profile.Label = state.Active.Label
			}
			_ = writeJSON(filepath.Join(profDir, "profile.json"), mf)
		}
		_ = a.SaveClaudeUIState(profile.ID)
		return a.buildClaudeState()
	}

	vaultBase, err := claudeVaultBase()
	if err != nil {
		return AppState{}, err
	}

	id := makeProfileID(name, time.Now())
	dir := filepath.Join(vaultBase, "profiles", id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return AppState{}, err
	}

	files := make([]string, 0)
	for _, file := range state.Files {
		if !file.Exists {
			continue
		}
		if err := copyFile(file.Path, filepath.Join(dir, file.Name)); err != nil {
			return AppState{}, fmt.Errorf("%s: %w", fmt.Sprintf(tr("保存 %s 失败", "failed to save %s"), file.Name), err)
		}
		files = append(files, file.Name)
	}
	if len(files) == 0 {
		return AppState{}, errors.New(tr("没有找到可保存的 Claude 认证文件：~/.claude/.credentials.json 或 auth.json", "no Claude auth file found (~/.claude/.credentials.json or auth.json)"))
	}

	// 快照 ~/.claude.json 中的 oauthAccount，激活时恢复完整账号上下文
	// （organizationUuid、organizationName、email 等）。
	if oauthAccount := readActiveOAuthAccount(); oauthAccount != nil {
		_ = saveProfileOAuthAccount(dir, oauthAccount)
	}

	now := time.Now().Format(time.RFC3339)
	manifest := profileManifest{
		Profile: Profile{
			ID:          id,
			Name:        name,
			Label:       state.Active.Label,
			CreatedAt:   now,
			UpdatedAt:   now,
			Fingerprint: state.Active.Fingerprint,
			AccountID:   state.Active.AccountID,
			AuthMode:    state.Active.AuthMode,
			Plan:        state.Active.Plan,
			Quota:       state.Active.Quota,
			Files:       files,
		},
	}
	if err := writeJSON(filepath.Join(dir, "profile.json"), manifest); err != nil {
		return AppState{}, err
	}

	_ = a.SaveClaudeUIState(id)
	return a.buildClaudeState()
}

func listClaudeProfiles(root string) ([]Profile, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	profiles := make([]Profile, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		manifest, err := readProfile(filepath.Join(root, entry.Name()))
		if err != nil {
			continue
		}
		profileDir := filepath.Join(root, entry.Name())
		summary := summarizeClaudeAccount(inspectClaudeFiles(profileDir))
		enrichProfileFromSummary(&manifest.Profile, summary)
		usage, _ := cachedFetchClaudeUsage(profileDir)
		manifest.Profile.Usage = usage
		profiles = append(profiles, manifest.Profile)
	}
	sort.Slice(profiles, func(i, j int) bool {
		return profiles[i].UpdatedAt > profiles[j].UpdatedAt
	})
	return profiles, nil
}


