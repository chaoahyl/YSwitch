package main

import (
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type App struct {
	ctx context.Context
}

type ManagedFile struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
	Size   int64  `json:"size"`
}

type Profile struct {
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	Label       string        `json:"label,omitempty"`
	CreatedAt   string        `json:"createdAt"`
	UpdatedAt   string        `json:"updatedAt"`
	Fingerprint string        `json:"fingerprint"`
	AccountID   string        `json:"accountId"`
	AuthMode    string        `json:"authMode"`
	Plan        string        `json:"plan"`
	Quota       string        `json:"quota"`
	Files       []string      `json:"files"`
	Usage       UsageSnapshot `json:"usage"`
}

type AccountSummary struct {
	Label             string `json:"label"`
	Fingerprint       string `json:"fingerprint"`
	UpdatedAt         string `json:"updatedAt"`
	AuthMode          string `json:"authMode"`
	AccountID         string `json:"accountId"`
	Plan              string `json:"plan"`
	Quota             string `json:"quota"`
	EntitlementSource string `json:"entitlementSource"`
}

type RateLimitWindow struct {
	LimitID            string  `json:"limitId"`
	LimitName          string  `json:"limitName"`
	UsedPercent        float64 `json:"usedPercent"`
	WindowDurationMins int     `json:"windowDurationMins"`
	ResetsAt           string  `json:"resetsAt"`
}

type CreditsSummary struct {
	HasCredits bool   `json:"hasCredits"`
	Unlimited  bool   `json:"unlimited"`
	Balance    string `json:"balance"`
}

type UsageSnapshot struct {
	Source    string            `json:"source"`
	Status    string            `json:"status"`
	PlanType  string            `json:"planType"`
	AccountID string            `json:"accountId"`
	Label     string            `json:"label,omitempty"`
	UpdatedAt string            `json:"updatedAt"`
	Windows   []RateLimitWindow `json:"windows"`
	Credits   CreditsSummary    `json:"credits"`
}

type UIState struct {
	SelectedProfileID string `json:"selectedProfileId"`
	HasActivated      bool   `json:"hasActivated"`
}

type AppState struct {
	CodexDir      string         `json:"codexDir"`
	VaultDir      string         `json:"vaultDir"`
	Active        AccountSummary `json:"active"`
	Files         []ManagedFile  `json:"files"`
	Profiles      []Profile      `json:"profiles"`
	Usage         UsageSnapshot  `json:"usage"`
	RestartStatus string         `json:"restartStatus"`
	UIState       UIState        `json:"uiState"`
}

type profileManifest struct {
	Profile
}

func NewApp() *App {
	return &App{}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

func switchBaseDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".yswitch"), nil
}

func claudeConfigBaseDir() (string, error) {
	base, err := switchBaseDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "claude"), nil
}

func legacyClaudeConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude"), nil
}


func extractAuthDetails(data []byte) AccountSummary {
	details := AccountSummary{
		Label:             "",
		Fingerprint:       "",
		UpdatedAt:         "",
		AuthMode:          "chatgpt",
		AccountID:         "",
		Plan:              "",
		Quota:             "",
		EntitlementSource: "",
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return details
	}

	if mode, ok := raw["auth_mode"].(string); ok && strings.TrimSpace(mode) != "" {
		details.AuthMode = mode
	}

	if tokens, ok := raw["tokens"].(map[string]any); ok {
		if updated := stringFromMap(tokens, "updated_at", "last_refresh", "created_at"); updated != "" {
			details.UpdatedAt = updated
		}
		details.AccountID = stringFromMap(tokens, "account_id", "accountId")

		if accessToken := stringFromMap(tokens, "id_token", "access_token"); accessToken != "" {
			claims := decodeJWTClaims(accessToken)
			authClaims := openAIAuthClaims(claims)
			claimSummary := AccountSummary{
				Label:       extractAccountLabel(data),
				AccountID:   stringFromMap(authClaims, "chatgpt_account_id", "chatgpt_account_user_id", "account_id"),
				Plan:        stringFromMap(authClaims, "chatgpt_plan_type", "plan_type", "plan", "subscription"),
				Quota:       quotaFromClaims(claims),
				UpdatedAt:   stringFromMap(claims, "updated_at"),
				Fingerprint: "",
			}
			mergeAccountDetails(&details, claimSummary)
			if details.AccountID == "" {
				details.AccountID = claimSummary.AccountID
			}
		}
	}

	if details.Label == "" {
		details.Label = extractAccountLabel(data)
	}
	return details
}

func extractAccountLabel(data []byte) string {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return ""
	}
	if tokens, ok := raw["tokens"].(map[string]any); ok {
		for _, tokenKey := range []string{"access_token", "id_token"} {
			if token := stringFromMap(tokens, tokenKey); token != "" {
				claims := decodeJWTClaims(token)
				if profileClaims := openAIProfileClaims(claims); profileClaims != nil {
					for _, key := range []string{"email", "preferred_username", "name"} {
						if value := stringFromMap(profileClaims, key); value != "" {
							return value
						}
					}
				}
				for _, key := range []string{"email", "preferred_username", "name"} {
					if value := stringFromMap(claims, key); value != "" {
						return value
					}
				}
			}
		}
	}
	for _, key := range []string{"email", "name", "username"} {
		if value := stringFromMap(raw, key); value != "" {
			return value
		}
	}
	return ""
}

func mergeAccountDetails(target *AccountSummary, source AccountSummary) {
	if target.Label == "" && source.Label != "" {
		target.Label = source.Label
	}
	if target.AccountID == "" && source.AccountID != "" {
		target.AccountID = source.AccountID
	}
	if target.Plan == "" {
		if source.Plan != "" {
			target.Plan = source.Plan
		}
	}
	if target.Quota == "" {
		if source.Quota != "" {
			target.Quota = source.Quota
		}
	}
	if target.UpdatedAt == "" && source.UpdatedAt != "" {
		target.UpdatedAt = source.UpdatedAt
	}
}

func decodeJWTClaims(token string) map[string]any {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return nil
	}
	data, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil
	}
	var claims map[string]any
	if err := json.Unmarshal(data, &claims); err != nil {
		return nil
	}
	return claims
}

func stringFromMap(values map[string]any, keys ...string) string {
	if values == nil {
		return ""
	}
	for _, key := range keys {
		if raw, ok := values[key]; ok {
			switch value := raw.(type) {
			case string:
				if strings.TrimSpace(value) != "" {
					return value
				}
			case fmt.Stringer:
				text := strings.TrimSpace(value.String())
				if text != "" {
					return text
				}
			}
		}
	}
	return ""
}

func quotaFromClaims(claims map[string]any) string {
	if claims == nil {
		return ""
	}
	if authClaims := openAIAuthClaims(claims); authClaims != nil {
		for _, key := range []string{"quota_remaining", "remaining_quota", "credit_balance"} {
			if raw, ok := authClaims[key]; ok {
				return fmt.Sprintf("%v", raw)
			}
		}
	}
	for _, key := range []string{"quota_remaining", "remaining_quota", "credit_balance"} {
		if raw, ok := claims[key]; ok {
			return fmt.Sprintf("%v", raw)
		}
	}
	return ""
}

func openAIAuthClaims(claims map[string]any) map[string]any {
	return mapFromMap(claims, "https://api.openai.com/auth")
}

func openAIProfileClaims(claims map[string]any) map[string]any {
	return mapFromMap(claims, "https://api.openai.com/profile")
}

func mapFromMap(values map[string]any, keys ...string) map[string]any {
	if values == nil {
		return nil
	}
	for _, key := range keys {
		if raw, ok := values[key]; ok {
			if nested, ok := raw.(map[string]any); ok {
				return nested
			}
		}
	}
	return nil
}

func readProfile(dir string) (profileManifest, error) {
	var manifest profileManifest
	data, err := os.ReadFile(filepath.Join(dir, "profile.json"))
	if err != nil {
		return manifest, err
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return manifest, err
	}
	return manifest, nil
}

func profileMatchesActive(active AccountSummary, profile Profile) bool {
	if strings.TrimSpace(active.AccountID) != "" && strings.TrimSpace(active.AccountID) == strings.TrimSpace(profile.AccountID) {
		return true
	}
	if strings.TrimSpace(active.Fingerprint) != "" && strings.TrimSpace(active.Fingerprint) == strings.TrimSpace(profile.Fingerprint) {
		return true
	}
	trimLabel := strings.TrimSpace(active.Label)
	if trimLabel == "" {
		return false
	}
	return trimLabel == strings.TrimSpace(profile.Name) || trimLabel == strings.TrimSpace(profile.Label)
}

func makeProfileID(name string, now time.Time) string {
	clean := strings.ToLower(strings.TrimSpace(name))
	clean = strings.ReplaceAll(clean, " ", "-")
	clean = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			return r
		}
		return -1
	}, clean)
	if clean == "" {
		clean = "account"
	}
	if len(clean) > 24 {
		clean = clean[:24]
	}
	return fmt.Sprintf("%s-%s", now.Format("20060102-150405"), clean)
}

func copyFile(src string, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

const maxBackupSlots = 5

// moveFilesAside 将每个已存在的文件移入 backupDir 下的时间戳子目录，
// 并保留最多 maxBackupSlots 个备份槽，超出时删除最旧的。
// 优先使用 Rename；若失败（如跨设备）则先复制再删除。
func moveFilesAside(filePaths []string, backupDir string) error {
	ts := time.Now().Format("20060102-150405.000000000")
	slotDir := filepath.Join(backupDir, ts)
	created := false
	for _, src := range filePaths {
		if _, err := os.Stat(src); err != nil {
			continue
		}
		if !created {
			if err := os.MkdirAll(slotDir, 0o700); err != nil {
				return err
			}
			created = true
		}
		dst := filepath.Join(slotDir, filepath.Base(src))
		if err := os.Rename(src, dst); err != nil {
			if copyErr := copyFile(src, dst); copyErr != nil {
				return fmt.Errorf("%s: %w", fmt.Sprintf(tr("备份 %s 失败", "failed to back up %s"), filepath.Base(src)), copyErr)
			}
			_ = os.Remove(src)
		}
	}
	if created {
		pruneBackupSlots(backupDir, maxBackupSlots)
	}
	return nil
}

func pruneBackupSlots(dir string, keep int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	dirs := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, e.Name())
		}
	}
	if len(dirs) <= keep {
		return
	}
	sort.Strings(dirs)
	for _, name := range dirs[:len(dirs)-keep] {
		_ = os.RemoveAll(filepath.Join(dir, name))
	}
}

func fingerprintForFiles(files []ManagedFile) string {
	hash := sha1.New()
	hasData := false
	for _, file := range files {
		if !file.Exists {
			continue
		}
		data, err := os.ReadFile(file.Path)
		if err != nil {
			continue
		}
		_, _ = io.WriteString(hash, file.Name)
		_, _ = hash.Write(data)
		hasData = true
	}
	if !hasData {
		return ""
	}
	return strings.ToUpper(hex.EncodeToString(hash.Sum(nil)))
}

func sortProfilesByUpdatedAt(profiles []Profile) {
	sort.SliceStable(profiles, func(i, j int) bool {
		return profiles[i].UpdatedAt > profiles[j].UpdatedAt
	})
}
