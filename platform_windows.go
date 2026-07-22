//go:build windows

package main

import (
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

func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
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
