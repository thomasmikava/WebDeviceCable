package bridge

import (
	"errors"
	"golang.org/x/sys/windows/registry"
	"os"
	"path/filepath"
)

func claudeOSPolicy() error {
	for _, root := range []registry.Key{registry.LOCAL_MACHINE, registry.CURRENT_USER} {
		key, err := registry.OpenKey(root, `SOFTWARE\Policies\ClaudeCode`, registry.QUERY_VALUE)
		if errors.Is(err, registry.ErrNotExist) {
			continue
		}
		if err != nil {
			return fail(503, "native_policy_unavailable", "Claude OS policy could not be checked safely.")
		}
		_, _, err = key.GetStringValue("Settings")
		key.Close()
		if !errors.Is(err, registry.ErrNotExist) {
			return fail(503, "native_policy_unavailable", "Claude has OS-managed policy that WDC cannot isolate.")
		}
	}
	root := os.Getenv("ProgramFiles")
	if root == "" {
		root = `C:\Program Files`
	}
	return claudePolicyDirectory(filepath.Join(root, "ClaudeCode"))
}
