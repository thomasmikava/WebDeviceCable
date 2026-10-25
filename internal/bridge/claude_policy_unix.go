//go:build !windows

package bridge

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func claudeOSPolicy() error {
	if runtime.GOOS == "darwin" {
		for _, path := range []string{"/Library/Managed Preferences/com.anthropic.claudecode.plist", filepath.Join("/Library/Managed Preferences", os.Getenv("USER"), "com.anthropic.claudecode.plist")} {
			if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
				return fail(503, "native_policy_unavailable", "Claude has OS-managed policy that WDC cannot isolate.")
			}
		}
		return claudePolicyDirectory("/Library/Application Support/ClaudeCode")
	}
	if release, err := os.ReadFile("/proc/sys/kernel/osrelease"); err == nil && strings.Contains(strings.ToLower(string(release)), "microsoft") {
		return fail(503, "native_policy_unavailable", "Claude policy isolation in WSL is not validated. Use the Windows-native WDC and Claude clients.")
	}
	return claudePolicyDirectory("/etc/claude-code")
}
