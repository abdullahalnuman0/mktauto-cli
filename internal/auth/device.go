package auth

import (
	"bufio"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/abdullahalnuman0/mktauto-cli/internal/api"
)

func osName() string { return runtime.GOOS }

// ThisDevice describes this computer for the sign-in (display only on the website).
func ThisDevice(version string) api.Device {
	host, _ := os.Hostname()
	return api.Device{OS: runtime.GOOS, OSVersion: osVersion(), Arch: runtime.GOARCH, Hostname: host, CLIVersion: version}
}

func osVersion() string {
	switch runtime.GOOS {
	case "linux":
		f, err := os.Open("/etc/os-release")
		if err != nil {
			return ""
		}
		defer f.Close()
		s := bufio.NewScanner(f)
		for s.Scan() {
			if v, ok := strings.CutPrefix(s.Text(), "PRETTY_NAME="); ok {
				return strings.Trim(v, `"`)
			}
		}
	case "darwin":
		return run("sw_vers", "-productVersion")
	case "windows":
		// "Microsoft Windows [Version 10.0.26100.1234]" → "10.0.26100.1234"
		out := run("cmd", "/c", "ver")
		if i := strings.Index(out, "Version "); i >= 0 {
			return strings.TrimSuffix(out[i+len("Version "):], "]")
		}
		return out
	}
	return ""
}

func run(name string, args ...string) string {
	cmd := exec.Command(name, args...)
	done := make(chan []byte, 1)
	go func() {
		out, _ := cmd.Output()
		done <- out
	}()
	select {
	case out := <-done:
		return strings.TrimSpace(string(out))
	case <-time.After(2 * time.Second):
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		return ""
	}
}

// OpenBrowser opens a URL in the default browser; false when that isn't possible (e.g. over SSH).
func OpenBrowser(url string) bool {
	if os.Getenv("MKTAUTO_NO_BROWSER") != "" {
		return false
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
			return false
		}
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start() == nil
}
