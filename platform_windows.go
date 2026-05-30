//go:build windows

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// advapi32 credential manager procs
var (
	modAdvapi32     = syscall.NewLazyDLL("advapi32.dll")
	procCredEnum    = modAdvapi32.NewProc("CredEnumerateW")
	procCredRead    = modAdvapi32.NewProc("CredReadW")
	procCredDelete  = modAdvapi32.NewProc("CredDeleteW")
	procCredWrite   = modAdvapi32.NewProc("CredWriteW")
	procCredFreePtr = modAdvapi32.NewProc("CredFree")
)

// winCREDENTIAL mirrors the 64-bit Windows CREDENTIALW struct.
// Field order and padding must match exactly; do not reorder.
type winCREDENTIAL struct {
	Flags              uint32
	Type               uint32
	TargetName         *uint16
	Comment            *uint16
	LastWritten        [2]uint32 // FILETIME
	CredentialBlobSize uint32
	_                  uint32 // padding: aligns CredentialBlob to 8-byte boundary
	CredentialBlob     *byte
	Persist            uint32
	AttributeCount     uint32
	Attributes         uintptr
	TargetAlias        *uint16
	UserName           *uint16
}

// stopVSCode finds running VSCode instances, records the launcher path, and terminates them.
func stopVSCode() (launcherPath string, err error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return "", fmt.Errorf("%s: %w", tr("VSCode 关闭失败", "failed to close VSCode"), err)
	}
	defer windows.CloseHandle(snapshot)

	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))

	var pids []uint32
	if e := windows.Process32First(snapshot, &entry); e != nil {
		return findVSCodeLauncher(), nil
	}
	for {
		if strings.EqualFold(windows.UTF16ToString(entry.ExeFile[:]), "code.exe") {
			if launcherPath == "" {
				launcherPath = queryProcessPath(entry.ProcessID)
			}
			pids = append(pids, entry.ProcessID)
		}
		if e := windows.Process32Next(snapshot, &entry); e != nil {
			break
		}
	}

	for _, pid := range pids {
		if h, e := windows.OpenProcess(windows.PROCESS_TERMINATE, false, pid); e == nil {
			_ = windows.TerminateProcess(h, 1)
			windows.CloseHandle(h)
		}
	}
	if len(pids) > 0 {
		time.Sleep(2 * time.Second)
	}
	if launcherPath == "" {
		launcherPath = findVSCodeLauncher()
	}
	return launcherPath, nil
}

func queryProcessPath(pid uint32) string {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)
	var buf [windows.MAX_PATH]uint16
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &size); err != nil {
		return ""
	}
	return windows.UTF16ToString(buf[:size])
}

func findVSCodeLauncher() string {
	for _, env := range []string{"LOCALAPPDATA", "ProgramFiles"} {
		base := os.Getenv(env)
		if base == "" {
			continue
		}
		for _, rel := range []string{
			filepath.Join("Programs", "Microsoft VS Code", "Code.exe"),
			filepath.Join("Microsoft VS Code", "Code.exe"),
		} {
			if p := filepath.Join(base, rel); pathExists(p) {
				return p
			}
		}
	}
	return ""
}

func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// startVSCode launches VSCode directly without spawning a hidden shell.
func startVSCode(launcherPath string) error {
	if launcherPath == "" {
		launcherPath = findVSCodeLauncher()
	}
	if launcherPath == "" {
		if p, e := exec.LookPath("code"); e == nil {
			launcherPath = p
		} else {
			return fmt.Errorf(tr("VSCode 启动失败: 未找到安装路径", "failed to start VSCode: not found"))
		}
	}
	cmd := exec.Command(launcherPath)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP,
	}
	return cmd.Start()
}

// windowsAnthropicDirs returns directories where the VSCode Claude extension stores credentials.
func windowsAnthropicDirs() []string {
	appData := os.Getenv("APPDATA")
	if appData == "" {
		return nil
	}
	candidates := []string{
		filepath.Join(appData, "Anthropic"),
		filepath.Join(appData, "Anthropic", "claude-code"),
	}
	var result []string
	for _, d := range candidates {
		if _, err := os.Stat(d); err == nil {
			result = append(result, d)
		}
	}
	return result
}

// updateWindowsCredentialManager updates Claude/Anthropic entries in Windows Credential Manager
// so VSCode reads the new tokens when it restarts. Falls back to deletion (VSCode re-reads files).
func updateWindowsCredentialManager(profileDir string) {
	data, err := os.ReadFile(filepath.Join(profileDir, ".credentials.json"))
	if err != nil {
		return
	}
	var credData map[string]any
	if json.Unmarshal(data, &credData) != nil {
		return
	}
	oauth := mapFromMap(credData, "claudeAiOauth", "claude_ai_oauth", "oauth")
	if oauth == nil {
		return
	}
	newAT := stringFromMap(oauth, "accessToken", "access_token")
	newRT := stringFromMap(oauth, "refreshToken", "refresh_token")
	if newAT == "" {
		return
	}

	var count uint32
	var credArray **winCREDENTIAL
	ret, _, _ := procCredEnum.Call(0, 0,
		uintptr(unsafe.Pointer(&count)),
		uintptr(unsafe.Pointer(&credArray)),
	)
	if ret == 0 || count == 0 {
		return
	}
	defer procCredFreePtr.Call(uintptr(unsafe.Pointer(credArray)))

	type credMatch struct {
		name     string
		credType uint32
	}
	var matches []credMatch
	for _, c := range unsafe.Slice(credArray, count) {
		if c == nil || c.TargetName == nil {
			continue
		}
		target := windows.UTF16PtrToString(c.TargetName)
		tl := strings.ToLower(target)
		if strings.Contains(tl, "claude") || strings.Contains(tl, "anthropic") {
			matches = append(matches, credMatch{name: target, credType: c.Type})
		}
	}

	for _, m := range matches {
		if !patchWindowsCredential(m.name, m.credType, newAT, newRT) {
			if tPtr, e := windows.UTF16PtrFromString(m.name); e == nil {
				procCredDelete.Call(uintptr(unsafe.Pointer(tPtr)), uintptr(m.credType), 0)
			}
		}
	}
}

// patchWindowsCredential reads a credential, updates the OAuth tokens in-place, and writes it back.
func patchWindowsCredential(target string, credType uint32, newAT, newRT string) bool {
	tPtr, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return false
	}
	var credPtr *winCREDENTIAL
	ret, _, _ := procCredRead.Call(
		uintptr(unsafe.Pointer(tPtr)),
		uintptr(credType),
		0,
		uintptr(unsafe.Pointer(&credPtr)),
	)
	if ret == 0 || credPtr == nil {
		return false
	}
	defer procCredFreePtr.Call(uintptr(unsafe.Pointer(credPtr)))

	if credPtr.CredentialBlobSize == 0 || credPtr.CredentialBlob == nil {
		return false
	}
	oldPwd := decodeCredBlob(unsafe.Slice(credPtr.CredentialBlob, credPtr.CredentialBlobSize))
	newPwd := buildUpdatedCredential(oldPwd, newAT, newRT)
	if newPwd == "" {
		return false
	}
	newBlob := encodeCredBlob(newPwd)
	if len(newBlob) == 0 {
		return false
	}

	updated := *credPtr
	updated.CredentialBlobSize = uint32(len(newBlob))
	updated.CredentialBlob = &newBlob[0]
	r, _, _ := procCredWrite.Call(uintptr(unsafe.Pointer(&updated)), 0)
	return r != 0
}

func decodeCredBlob(blob []byte) string {
	if len(blob) >= 2 && len(blob)%2 == 0 {
		u16 := make([]uint16, len(blob)/2)
		for i := range u16 {
			u16[i] = uint16(blob[i*2]) | uint16(blob[i*2+1])<<8
		}
		if s := windows.UTF16ToString(u16); s != "" {
			return s
		}
	}
	return string(blob)
}

func encodeCredBlob(s string) []byte {
	u16, err := windows.UTF16FromString(s)
	if err != nil || len(u16) <= 1 {
		return nil
	}
	u16 = u16[:len(u16)-1] // strip null terminator
	blob := make([]byte, len(u16)*2)
	for i, c := range u16 {
		blob[i*2] = byte(c)
		blob[i*2+1] = byte(c >> 8)
	}
	return blob
}

func buildUpdatedCredential(oldPwd, newAT, newRT string) string {
	if oldPwd == "" {
		return newAT
	}
	var d map[string]any
	if json.Unmarshal([]byte(oldPwd), &d) != nil {
		return newAT
	}
	if _, ok := d["accessToken"]; ok {
		d["accessToken"] = newAT
	}
	if _, ok := d["refreshToken"]; ok && newRT != "" {
		d["refreshToken"] = newRT
	}
	if nested, ok := d["claudeAiOauth"].(map[string]any); ok {
		if _, ok2 := nested["accessToken"]; ok2 {
			nested["accessToken"] = newAT
		}
		if _, ok2 := nested["refreshToken"]; ok2 && newRT != "" {
			nested["refreshToken"] = newRT
		}
	}
	out, err := json.Marshal(d)
	if err != nil {
		return newAT
	}
	return string(out)
}

// stopCodex finds running Codex instances, records the launcher path, and terminates them.
func stopCodex() (launcherPath string, err error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return "", fmt.Errorf("%s: %w", tr("Codex 关闭失败", "failed to close Codex"), err)
	}
	defer windows.CloseHandle(snapshot)

	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))

	var pids []uint32
	if e := windows.Process32First(snapshot, &entry); e != nil {
		return findCodexInstallPath(), nil
	}
	for {
		if strings.EqualFold(windows.UTF16ToString(entry.ExeFile[:]), "codex.exe") {
			if launcherPath == "" {
				launcherPath = queryProcessPath(entry.ProcessID)
			}
			pids = append(pids, entry.ProcessID)
		}
		if e := windows.Process32Next(snapshot, &entry); e != nil {
			break
		}
	}

	for _, pid := range pids {
		if h, e := windows.OpenProcess(windows.PROCESS_TERMINATE, false, pid); e == nil {
			_ = windows.TerminateProcess(h, 1)
			windows.CloseHandle(h)
		}
	}
	if len(pids) > 0 {
		time.Sleep(2 * time.Second)
	}
	if launcherPath == "" {
		launcherPath = findCodexInstallPath()
	}
	return launcherPath, nil
}

func findCodexInstallPath() string {
	localAppData := os.Getenv("LOCALAPPDATA")
	for _, rel := range []string{
		filepath.Join("Programs", "codex", "Codex.exe"),
		filepath.Join("Programs", "Codex", "Codex.exe"),
	} {
		if p := filepath.Join(localAppData, rel); pathExists(p) {
			return p
		}
	}
	return ""
}

// startCodex launches Codex. For Store installs it uses the shell:AppsFolder protocol via explorer.exe.
func startCodex(launcherPath string) error {
	// Non-Store install: launch directly.
	if launcherPath != "" && !strings.Contains(strings.ToLower(launcherPath), "windowsapps") {
		cmd := exec.Command(launcherPath)
		cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP}
		return cmd.Start()
	}

	// Try common non-Store paths first.
	if p := findCodexInstallPath(); p != "" {
		cmd := exec.Command(p)
		cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP}
		return cmd.Start()
	}

	// Store install: resolve AUMID from registry and launch via explorer.
	if aumid := findCodexAUMID(); aumid != "" {
		return exec.Command("explorer.exe", "shell:AppsFolder\\"+aumid).Start()
	}

	return fmt.Errorf(tr("Codex 启动失败: 未找到安装路径", "failed to start Codex: not found"))
}

// findCodexAUMID searches the AppModel package repository for a Codex package and returns its AUMID.
func findCodexAUMID() string {
	const packagesKey = `Software\Classes\Local Settings\Software\Microsoft\Windows\CurrentVersion\AppModel\Repository\Packages`
	k, err := registry.OpenKey(registry.CURRENT_USER, packagesKey, registry.ENUMERATE_SUB_KEYS)
	if err != nil {
		return ""
	}
	defer k.Close()

	names, _ := k.ReadSubKeyNames(-1)
	for _, name := range names {
		if !strings.Contains(strings.ToLower(name), "codex") {
			continue
		}
		// Package full name: Publisher.App_Version_Arch_Resource_PublisherHash
		// Family name:       Publisher.App_PublisherHash
		parts := strings.Split(name, "_")
		if len(parts) < 2 {
			continue
		}
		familyName := parts[0] + "_" + parts[len(parts)-1]

		// Enumerate app IDs inside the package (usually just "App").
		pk, err := registry.OpenKey(k, name, registry.ENUMERATE_SUB_KEYS)
		if err != nil {
			return familyName + "!App"
		}
		appIDs, _ := pk.ReadSubKeyNames(-1)
		pk.Close()
		if len(appIDs) > 0 {
			return familyName + "!" + appIDs[0]
		}
		return familyName + "!App"
	}
	return ""
}
