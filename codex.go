package main

import (
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

var managedFileNames = []string{
	"auth.json",
}

var (
	codexUsageCache   = map[string]usageCacheEntry{}
	codexUsageCacheMu sync.Mutex
)

func cachedFetchCodexUsage(codexDir string) (UsageSnapshot, error) {
	token, _, _, err := readAccessToken(codexDir)
	if err != nil || token == "" {
		return fetchUsageFromAuth(codexDir)
	}

	codexUsageCacheMu.Lock()
	if entry, ok := codexUsageCache[token]; ok && time.Now().Before(entry.expiresAt) {
		codexUsageCacheMu.Unlock()
		return entry.snapshot, nil
	}
	codexUsageCacheMu.Unlock()

	snapshot, fetchErr := fetchUsageFromAuth(codexDir)
	if fetchErr == nil {
		codexUsageCacheMu.Lock()
		pruneExpiredUsage(codexUsageCache)
		codexUsageCache[token] = usageCacheEntry{snapshot: snapshot, expiresAt: time.Now().Add(usageCacheTTL)}
		codexUsageCacheMu.Unlock()
	}
	return snapshot, fetchErr
}

func codexConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".codex"), nil
}

func codexVaultBase() (string, error) {
	base, err := switchBaseDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "codex"), nil
}

func uiStatePath() (string, error) {
	vaultDir, err := codexVaultBase()
	if err != nil {
		return "", err
	}
	return filepath.Join(vaultDir, "state.json"), nil
}

func (a *App) GetState() (AppState, error) {
	return a.buildState()
}

func (a *App) RefreshUsage() (UsageSnapshot, error) {
	codexDir, err := codexConfigDir()
	if err != nil {
		return UsageSnapshot{}, err
	}
	// 始终从实时凭证目录（~/.codex）读取：当前登录账号的令牌由 Codex 持续刷新，
	// 而已保存 profile 中的副本是保存时的快照，令牌通常已过期，直接请求会返回 401。
	return cachedFetchCodexUsage(codexDir)
}

func (a *App) RefreshAllUsage() (AppState, error) {
	return a.buildState()
}

func (a *App) SaveCurrentProfile(name string) (AppState, error) {
	return a.ImportCurrentAccount(name)
}

func (a *App) ImportCurrentAccount(name string) (AppState, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return AppState{}, errors.New(tr("请输入账号名称", "account name is required"))
	}

	state, err := a.buildState()
	if err != nil {
		return AppState{}, err
	}

	if profile, ok := findDuplicateProfile(state.Active, state.Profiles); ok {
		// 强匹配（fingerprint 相同）：token 未变，仅更新选中状态。
		isTokenMatch := state.Active.Fingerprint != "" && state.Active.Fingerprint == profile.Fingerprint
		if isTokenMatch {
			if err := a.SaveUIState(profile.ID); err != nil {
				return AppState{}, err
			}
			return a.buildState()
		}
		// 弱匹配（accountID/label 相同但 token 已变）：同账号重新登录，刷新 vault 凭证。
		profDir := filepath.Join(state.VaultDir, "profiles", profile.ID)
		for _, f := range state.Files {
			if !f.Exists {
				continue
			}
			if err := copyFile(f.Path, filepath.Join(profDir, f.Name)); err != nil {
				return AppState{}, fmt.Errorf("%s: %w", fmt.Sprintf(tr("更新凭证 %s 失败", "failed to update credential %s"), f.Name), err)
			}
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
		if err := a.SaveUIState(profile.ID); err != nil {
			return AppState{}, err
		}
		return a.buildState()
	}

	profilesDir := filepath.Join(state.VaultDir, "profiles")
	if err := os.MkdirAll(profilesDir, 0o700); err != nil {
		return AppState{}, err
	}

	id := makeProfileID(name, time.Now())
	dir := filepath.Join(profilesDir, id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return AppState{}, err
	}

	files := make([]string, 0, len(managedFileNames))
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
		return AppState{}, errors.New(tr("没有找到可保存的 ~/.codex/auth.json", "no auth file found (~/.codex/auth.json)"))
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

	if err := a.SaveUIState(id); err != nil {
		return AppState{}, err
	}
	return a.buildState()
}

func (a *App) ActivateProfile(id string) (AppState, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return AppState{}, errors.New(tr("请选择要切换的账号", "no account selected"))
	}

	state, err := a.buildState()
	if err != nil {
		return AppState{}, err
	}

	profileDir := filepath.Join(state.VaultDir, "profiles", id)
	manifest, err := readProfile(profileDir)
	if err != nil {
		return AppState{}, err
	}

	targetProfile := manifest.Profile
	enrichProfileFromSummary(&targetProfile, summarizeAccount(inspectFiles(profileDir)))
	if profileMatchesActive(state.Active, targetProfile) {
		return AppState{}, errors.New("already active")
	}

	// 修改凭证前先关闭 Codex，防止其退出时将旧 token 写回。
	launcher, stopErr := stopCodex()

	// 将实时凭证写回当前正在退出的 profile vault，确保下次切回时使用最新 token
	// （Codex 在账号使用期间会自动刷新 access token，存档快照否则会逐渐过期）。
	if state.UIState.SelectedProfileID != "" && state.UIState.SelectedProfileID != id {
		outDir := filepath.Join(state.VaultDir, "profiles", state.UIState.SelectedProfileID)
		if _, statErr := os.Stat(outDir); statErr == nil {
			for _, f := range state.Files {
				if f.Exists {
					_ = copyFile(f.Path, filepath.Join(outDir, f.Name))
				}
			}
		}
	}

	backupDir := filepath.Join(state.VaultDir, "saved-auth")
	replacements := make([]fileReplacement, 0, len(managedFileNames))
	for _, fileName := range managedFileNames {
		src := filepath.Join(profileDir, fileName)
		if _, err := os.Stat(src); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return AppState{}, err
		}
		replacements = append(replacements, fileReplacement{Src: src, Dst: filepath.Join(state.CodexDir, fileName)})
	}
	if err := replaceManagedFiles(replacements, backupDir); err != nil {
		return AppState{}, fmt.Errorf("%s: %w", tr("切换认证文件失败", "failed to switch auth files"), err)
	}

	restartStatus := "ok"
	if stopErr != nil {
		restartStatus = stopErr.Error()
	} else if err := startCodex(launcher); err != nil {
		restartStatus = err.Error()
	}

	_ = a.SaveUIState(id)
	next, err := a.buildState()
	if err != nil {
		return AppState{}, err
	}
	next.RestartStatus = restartStatus
	return next, nil
}

func loadUIState() UIState {
	path, err := uiStatePath()
	if err != nil {
		return UIState{}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return UIState{}
	}
	var state UIState
	if json.Unmarshal(data, &state) != nil {
		return UIState{}
	}
	return state
}

func (a *App) SaveUIState(profileID string) error {
	path, err := uiStatePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return writeJSON(path, UIState{
		SelectedProfileID: profileID,
		HasActivated:      strings.TrimSpace(profileID) != "",
	})
}

func (a *App) buildState() (AppState, error) {
	codexDir, err := codexConfigDir()
	if err != nil {
		return AppState{}, err
	}
	vaultDir, err := codexVaultBase()
	if err != nil {
		return AppState{}, err
	}
	if err := os.MkdirAll(codexDir, 0o700); err != nil {
		return AppState{}, err
	}
	if err := os.MkdirAll(vaultDir, 0o700); err != nil {
		return AppState{}, err
	}

	profilesDir := filepath.Join(vaultDir, "profiles")
	if err := os.MkdirAll(profilesDir, 0o700); err != nil {
		return AppState{}, err
	}

	uiState := loadUIState()
	files := inspectFiles(codexDir)
	active := summarizeAccount(files)

	// 始终使用实时凭证目录获取当前账号用量，确保手动替换的 auth 文件立即生效。
	usage, _ := cachedFetchCodexUsage(codexDir)

	profiles, err := listProfiles(profilesDir)
	if err != nil {
		return AppState{}, err
	}

	// 与当前实时登录账号匹配的 profile，使用实时凭证（~/.codex）的套餐与用量，
	// 避免展示已保存快照中的过期数据：账号保存后若发生套餐升级（free→plus）或
	// 令牌刷新，存档副本不会更新，导致账号管理页等级、额度与首页不一致。
	for i := range profiles {
		if !profileMatchesActive(active, profiles[i]) {
			continue
		}
		if usage.Status == "ok" || len(usage.Windows) > 0 {
			profiles[i].Usage = usage
		} else if usage.PlanType != "" {
			profiles[i].Usage.PlanType = usage.PlanType
		}
		if active.Plan != "" {
			profiles[i].Plan = active.Plan
		}
		if active.Quota != "" {
			profiles[i].Quota = active.Quota
		}
	}

	// 清除过期的 SelectedProfileID（如用户手动删除 auth.json 后重新登录），
	// 使 UI 反映真实的实时凭证。
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
			_ = a.SaveUIState("")
		}
	}

	return AppState{
		CodexDir: codexDir,
		VaultDir: vaultDir,
		Active:   active,
		Files:    files,
		Profiles: profiles,
		Usage:    usage,
		UIState:  uiState,
	}, nil
}

func inspectFiles(codexDir string) []ManagedFile {
	files := make([]ManagedFile, 0, len(managedFileNames))
	for _, name := range managedFileNames {
		path := filepath.Join(codexDir, name)
		info, err := os.Stat(path)
		files = append(files, ManagedFile{
			Name:   name,
			Path:   path,
			Exists: err == nil,
			Size:   fileSize(info, err),
		})
	}
	return files
}

func fileSize(info os.FileInfo, err error) int64 {
	if err != nil || info == nil {
		return 0
	}
	return info.Size()
}

func summarizeAccount(files []ManagedFile) AccountSummary {
	summary := AccountSummary{
		Label:             "",
		Fingerprint:       fingerprintForFiles(files),
		AuthMode:          "chatgpt",
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
		details := extractAuthDetails(data)
		if details.Label != "" {
			summary.Label = details.Label
		}
		if details.AccountID != "" {
			summary.AccountID = details.AccountID
		}
		if details.AuthMode != "" {
			summary.AuthMode = details.AuthMode
		}
		if details.Plan != "" {
			summary.Plan = details.Plan
		}
		if details.Quota != "" {
			summary.Quota = details.Quota
		}
		if details.UpdatedAt != "" {
			summary.UpdatedAt = details.UpdatedAt
		}
		if details.EntitlementSource != "" {
			summary.EntitlementSource = details.EntitlementSource
		}
	}

	return summary
}

func fetchUsageFromAuth(codexDir string) (UsageSnapshot, error) {
	token, accountID, planType, err := readAccessToken(codexDir)
	if err != nil {
		return UsageSnapshot{
			Source:    "codex",
			Status:    err.Error(),
			PlanType:  planType,
			AccountID: accountID,
			UpdatedAt: time.Now().Format(time.RFC3339),
		}, err
	}

	// 若 access token 的 JWT exp 已过期，跳过注定失败的网络请求，给出明确状态。
	// 非当前账号的存档 token 无法自动续期，过期属预期情况（与 Claude 行为对称）。
	if claims := decodeJWTClaims(token); claims != nil {
		if exp, ok := claims["exp"].(float64); ok && time.Now().Unix() > int64(exp) {
			msg := tr("认证已过期", "token expired")
			return UsageSnapshot{
				Source:    "codex",
				Status:    msg,
				PlanType:  planType,
				AccountID: accountID,
				UpdatedAt: time.Now().Format(time.RFC3339),
			}, errors.New(msg)
		}
	}

	endpoints := []string{
		"https://chatgpt.com/backend-api/codex/usage",
		"https://chatgpt.com/backend-api/wham/usage",
	}

	var lastErr error
	for _, endpoint := range endpoints {
		snapshot, reqErr := requestUsage(endpoint, token)
		if reqErr == nil {
			if snapshot.PlanType == "" {
				snapshot.PlanType = planType
			}
			if snapshot.AccountID == "" {
				snapshot.AccountID = accountID
			}
			if snapshot.Source == "" {
				snapshot.Source = "codex"
			}
			if snapshot.UpdatedAt == "" {
				snapshot.UpdatedAt = time.Now().Format(time.RFC3339)
			}
			return snapshot, nil
		}
		lastErr = reqErr
	}

	if lastErr == nil {
		lastErr = errors.New(tr("未能读取 Codex 额度", "failed to read Codex quota"))
	}
	return UsageSnapshot{
		Source:    "codex",
		Status:    lastErr.Error(),
		PlanType:  planType,
		AccountID: accountID,
		UpdatedAt: time.Now().Format(time.RFC3339),
	}, lastErr
}

func readAccessToken(codexDir string) (string, string, string, error) {
	data, err := os.ReadFile(filepath.Join(codexDir, "auth.json"))
	if err != nil {
		return "", "", "", errors.New(tr("未找到 ~/.codex/auth.json", "auth file not found (~/.codex/auth.json)"))
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return "", "", "", fmt.Errorf("%s: %w", tr("auth.json 解析失败", "failed to parse auth.json"), err)
	}

	tokens, _ := raw["tokens"].(map[string]any)
	accessToken := stringFromMap(tokens, "access_token")
	if accessToken == "" {
		return "", "", "", errors.New(tr("auth.json 中未找到 access_token", "access_token not found in auth.json"))
	}

	accountID := stringFromMap(tokens, "account_id", "accountId")
	planType := ""
	if claims := decodeJWTClaims(stringFromMap(tokens, "id_token", "access_token")); claims != nil {
		planType = stringFromMap(claims, "plan_type", "plan", "subscription")
		if accountID == "" {
			if authClaims := openAIAuthClaims(claims); authClaims != nil {
				accountID = stringFromMap(authClaims, "chatgpt_account_id", "chatgpt_account_user_id", "account_id")
			}
			if accountID == "" {
				accountID = stringFromMap(claims, "sub")
			}
		}
	}
	return accessToken, accountID, planType, nil
}

func requestUsage(endpoint string, token string) (UsageSnapshot, error) {
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return UsageSnapshot{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "codex-account-switcher")
	req.Header.Set("Origin", "https://chatgpt.com")
	req.Header.Set("Referer", "https://chatgpt.com/")
	client := &http.Client{Timeout: 18 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return UsageSnapshot{}, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return UsageSnapshot{}, err
	}
	if resp.StatusCode == 401 || resp.StatusCode == 403 || resp.StatusCode == 429 {
		msg := tr("账号已限流或认证失效", "rate limited or auth invalid")
		return UsageSnapshot{}, errors.New(msg)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return UsageSnapshot{}, fmt.Errorf(tr("额度接口返回 %d", "quota API returned %d"), resp.StatusCode)
	}

	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return UsageSnapshot{}, err
	}
	snapshot := parseUsagePayload(raw)
	if len(snapshot.Windows) == 0 && snapshot.Credits.Balance == "" && snapshot.PlanType == "" {
		return UsageSnapshot{}, fmt.Errorf(tr("%s 未返回可识别的额度字段", "%s returned no recognizable quota fields"), endpoint)
	}
	snapshot.Source = "codex"
	snapshot.Status = "ok"
	snapshot.UpdatedAt = time.Now().Format(time.RFC3339)
	return snapshot, nil
}

func parseUsagePayload(raw map[string]any) UsageSnapshot {
	snapshot := UsageSnapshot{
		Windows: make([]RateLimitWindow, 0, 4),
	}
	if result, ok := raw["result"].(map[string]any); ok {
		raw = result
	}

	snapshot.PlanType = stringFromMap(raw, "plan_type", "planType", "plan")
	snapshot.AccountID = stringFromMap(raw, "account_id", "accountId")
	snapshot.Credits = parseCredits(raw)

	if byID, ok := raw["rateLimitsByLimitId"].(map[string]any); ok {
		if codex, ok := byID["codex"].(map[string]any); ok {
			parseRateLimitBucket("codex", codex, &snapshot)
		} else {
			for key, value := range byID {
				if bucket, ok := value.(map[string]any); ok {
					parseRateLimitBucket(key, bucket, &snapshot)
				}
			}
		}
	}
	if limits, ok := raw["rateLimits"].(map[string]any); ok {
		parseRateLimitBucket("codex", limits, &snapshot)
	}
	if limits, ok := raw["rate_limits"].(map[string]any); ok {
		parseRateLimitBucket("codex", limits, &snapshot)
	}
	collectLooseWindows("", raw, &snapshot)
	sort.SliceStable(snapshot.Windows, func(i, j int) bool {
		return snapshot.Windows[i].WindowDurationMins < snapshot.Windows[j].WindowDurationMins
	})
	return snapshot
}

func parseCredits(raw map[string]any) CreditsSummary {
	var summary CreditsSummary
	if nested, ok := raw["credits"].(map[string]any); ok {
		summary.HasCredits = boolFromMap(nested, "has_credits", "hasCredits")
		summary.Unlimited = boolFromMap(nested, "unlimited", "is_unlimited")
		summary.Balance = stringFromMap(nested, "balance", "remaining", "available")
	}
	if summary.Balance == "" {
		summary.Balance = stringFromMap(raw, "creditBalance", "credit_balance")
	}
	return summary
}

func parseRateLimitBucket(limitID string, bucket map[string]any, snapshot *UsageSnapshot) {
	if snapshot.PlanType == "" {
		snapshot.PlanType = stringFromMap(bucket, "planType", "plan_type", "plan")
	}
	if snapshot.AccountID == "" {
		snapshot.AccountID = stringFromMap(bucket, "accountId", "account_id")
	}
	if snapshot.Credits.Balance == "" {
		if c := parseCredits(bucket); c.Balance != "" || c.HasCredits {
			snapshot.Credits = c
		}
	}
	for _, key := range []string{"primary", "secondary"} {
		if window, ok := bucket[key].(map[string]any); ok {
			addRateLimitWindow(limitID, key, window, snapshot)
		}
	}
	if byID, ok := bucket["by_id"].(map[string]any); ok {
		for key, value := range byID {
			if nested, ok := value.(map[string]any); ok {
				addRateLimitWindow(limitID, key, nested, snapshot)
			}
		}
		return
	}
	collectLooseWindows(limitID, bucket, snapshot)
}

func collectLooseWindows(limitID string, raw map[string]any, snapshot *UsageSnapshot) {
	for key, value := range raw {
		switch typed := value.(type) {
		case map[string]any:
			nextID := limitID
			if key == "codex" || strings.Contains(strings.ToLower(key), "codex") {
				nextID = key
			}
			addRateLimitWindow(nextID, key, typed, snapshot)
			collectLooseWindows(nextID, typed, snapshot)
		case []any:
			for _, item := range typed {
				if nested, ok := item.(map[string]any); ok {
					addRateLimitWindow(limitID, key, nested, snapshot)
					collectLooseWindows(limitID, nested, snapshot)
				}
			}
		}
	}
}

func addRateLimitWindow(limitID string, limitName string, raw map[string]any, snapshot *UsageSnapshot) {
	usedPercent, ok := floatFromMap(raw, "usedPercent", "used_percent", "usagePercent", "usage_percent")
	if !ok {
		return
	}
	duration := intFromMap(raw, "windowDurationMins", "window_duration_mins", "durationMins", "duration_mins", "window_minutes", "windowMinutes")
	if duration == 0 {
		if secs := intFromMap(raw, "limit_window_seconds", "windowDurationSecs", "window_duration_seconds"); secs > 0 {
			duration = secs / 60
		}
	}
	if limitID == "" {
		limitID = stringFromMap(raw, "limitId", "limit_id", "bucket")
	}
	if limitName == "" {
		limitName = stringFromMap(raw, "limitName", "limit_name", "name")
	}
	window := RateLimitWindow{
		LimitID:            limitID,
		LimitName:          limitName,
		UsedPercent:        usedPercent,
		WindowDurationMins: duration,
		ResetsAt:           resetsAtString(raw),
	}
	if !hasWindow(snapshot.Windows, window) {
		snapshot.Windows = append(snapshot.Windows, window)
	}
}

func hasWindow(windows []RateLimitWindow, next RateLimitWindow) bool {
	for _, current := range windows {
		if current.LimitID == next.LimitID && current.LimitName == next.LimitName && current.WindowDurationMins == next.WindowDurationMins {
			return true
		}
	}
	return false
}

func resetsAtString(raw map[string]any) string {
	for _, key := range []string{"resetsAt", "resets_at", "reset_at", "resetAt"} {
		value, ok := raw[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case string:
			if s := strings.TrimSpace(typed); s != "" {
				return s
			}
		case float64:
			if typed > 100000000000 {
				return time.UnixMilli(int64(typed)).Format(time.RFC3339)
			}
			return time.Unix(int64(typed), 0).Format(time.RFC3339)
		}
	}
	return ""
}

func floatFromMap(values map[string]any, keys ...string) (float64, bool) {
	for _, key := range keys {
		if raw, ok := values[key]; ok {
			switch value := raw.(type) {
			case float64:
				return value, true
			case int:
				return float64(value), true
			case int64:
				return float64(value), true
			case string:
				var parsed float64
				if _, err := fmt.Sscanf(strings.TrimSuffix(strings.TrimSpace(value), "%"), "%f", &parsed); err == nil {
					return parsed, true
				}
			}
		}
	}
	return 0, false
}

func intFromMap(values map[string]any, keys ...string) int {
	for _, key := range keys {
		if raw, ok := values[key]; ok {
			switch value := raw.(type) {
			case float64:
				return int(value)
			case int:
				return value
			case int64:
				return int(value)
			}
		}
	}
	return 0
}

func boolFromMap(values map[string]any, keys ...string) bool {
	for _, key := range keys {
		if raw, ok := values[key]; ok {
			switch value := raw.(type) {
			case bool:
				return value
			case string:
				return strings.EqualFold(value, "true")
			}
		}
	}
	return false
}

func listProfiles(root string) ([]Profile, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	dirs := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			dirs = append(dirs, entry.Name())
		}
	}

	// 并发处理每个 profile：已过期的归档 token 会被 JWT exp 检查瞬间短路（无网络），
	// 未过期的则发起真实额度请求。并发执行避免串行 N×18s 阻塞 GetState。
	results := make([]Profile, len(dirs))
	ok := make([]bool, len(dirs))
	var wg sync.WaitGroup
	for i, name := range dirs {
		wg.Add(1)
		go func(i int, name string) {
			defer wg.Done()
			dir := filepath.Join(root, name)
			manifest, err := readProfile(dir)
			if err != nil {
				return
			}
			summary := summarizeAccount(inspectFiles(dir))
			enrichProfileFromSummary(&manifest.Profile, summary)
			manifest.Profile.Usage, _ = cachedFetchCodexUsage(dir)
			results[i] = manifest.Profile
			ok[i] = true
		}(i, name)
	}
	wg.Wait()

	profiles := make([]Profile, 0, len(dirs))
	for i := range results {
		if ok[i] {
			profiles = append(profiles, results[i])
		}
	}
	sortProfilesByUpdatedAt(profiles)
	return profiles, nil
}

func enrichProfileFromSummary(profile *Profile, summary AccountSummary) {
	if profile.Fingerprint == "" {
		profile.Fingerprint = summary.Fingerprint
	}
	if profile.AccountID == "" {
		profile.AccountID = summary.AccountID
	}
	if profile.AuthMode == "" {
		profile.AuthMode = summary.AuthMode
	}
	if profile.Plan == "" {
		profile.Plan = summary.Plan
	}
	if profile.Quota == "" {
		profile.Quota = summary.Quota
	}
}

func findDuplicateProfile(active AccountSummary, profiles []Profile) (Profile, bool) {
	for _, profile := range profiles {
		if active.AccountID != "" && active.AccountID == profile.AccountID {
			return profile, true
		}
		if active.Fingerprint != "" && active.Fingerprint == profile.Fingerprint {
			return profile, true
		}
		if active.Label != "" && (active.Label == profile.Label || active.Label == profile.Name) {
			return profile, true
		}
	}
	return Profile{}, false
}

func (a *App) QuickImportAccount() (AppState, error) {
	state, err := a.buildState()
	if err != nil {
		return AppState{}, err
	}
	name := state.Active.Label
	if name == "" {
		name = "account"
	}
	if at := strings.Index(name, "@"); at > 0 {
		name = name[:at]
	}
	if runes := []rune(name); len(runes) > 24 {
		name = string(runes[:24])
	}
	return a.ImportCurrentAccount(name)
}
