// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"os"
	"runtime"
	"strings"
)

// detectWSL reports whether the process runs under the Windows Subsystem for
// Linux: GOOS is linux and either WSL_DISTRO_NAME is set or the kernel release
// mentions "microsoft" or "wsl". isWSL calls it with the real values.
func detectWSL(goos string, getenv func(string) string, readFile func(string) ([]byte, error)) bool {
	if goos != "linux" {
		return false
	}
	if getenv("WSL_DISTRO_NAME") != "" {
		return true
	}
	for _, p := range []string{"/proc/sys/kernel/osrelease", "/proc/version"} {
		data, err := readFile(p)
		if err != nil {
			continue
		}
		s := strings.ToLower(string(data))
		if strings.Contains(s, "microsoft") || strings.Contains(s, "wsl") {
			return true
		}
	}
	return false
}

// isWSL reports whether the current process runs under WSL.
func isWSL() bool {
	return detectWSL(runtime.GOOS, os.Getenv, os.ReadFile)
}

// browserCommands returns the commands to try, in order, to open url. Under
// WSL there is usually no Linux desktop, so the Windows openers (wslview, then
// PowerShell and cmd) come before xdg-open.
func browserCommands(goos string, wsl bool, url string) [][]string {
	if wsl {
		return [][]string{
			{"wslview", url},
			{"powershell.exe", "-NoProfile", "-Command", "Start-Process", url},
			{"cmd.exe", "/c", "start", "", url},
			{"xdg-open", url},
		}
	}
	switch goos {
	case "linux":
		return [][]string{{"xdg-open", url}}
	case "darwin":
		return [][]string{{"open", url}}
	case "windows":
		return [][]string{{"rundll32", "url.dll,FileProtocolHandler", url}}
	}
	return nil
}

// wslDoctorNotes returns the WSL guidance `labctl doctor` prints. A Windows
// browser resolves ingress hostnames with the Windows hosts file, so `labctl
// hosts add` inside WSL is not enough on its own.
func wslDoctorNotes() []string {
	return []string{
		"WSL detected. The web UI (labctl ui, http://localhost:3939) works as-is via WSL2 localhost forwarding.",
		"Ingress hostnames (e.g. http://grafana.k3d.local) opened in a Windows browser use the WINDOWS hosts file,",
		"  not WSL's /etc/hosts. Add the same entries to C:\\Windows\\System32\\drivers\\etc\\hosts (as Administrator),",
		"  or reach services from inside WSL (curl) where 'labctl hosts add' applies.",
		"If WSL keeps overwriting /etc/hosts on restart, set 'generateHosts=false' under [network] in /etc/wsl.conf.",
		"Ensure Docker Desktop's WSL integration is enabled for this distro (or run a native docker daemon in WSL).",
	}
}
