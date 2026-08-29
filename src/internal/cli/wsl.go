// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"os"
	"runtime"
	"strings"
)

// detectWSL reports whether we are running inside the Windows Subsystem for
// Linux. It is pure (all inputs injected) so it can be unit-tested; isWSL wires
// it to the real environment.
//
// WSL is only possible when GOOS is linux. We treat the environment as WSL if
// the WSL_DISTRO_NAME variable is set (WSL2 sets it for interactive shells) or
// if the kernel release/version string mentions "microsoft" or "wsl" — the
// signature both WSL1 and WSL2 kernels carry.
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

// browserCommands returns the candidate opener commands to try, in order, for a
// given platform. Under WSL the Linux openers (xdg-open) usually fail because
// there is no Linux desktop, so we reach out to Windows first (wslview from the
// wslu package, then PowerShell/cmd) and only fall back to xdg-open for the rare
// case of a WSL distro running a Linux GUI.
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

// wslDoctorNotes returns the WSL-specific guidance printed by `labctl doctor`
// when running under WSL. The UI at :3939 works over WSL2 localhost forwarding,
// but ingress hostnames opened in a Windows browser resolve against the Windows
// hosts file, not the WSL one — so `labctl hosts add` inside WSL is not enough.
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
