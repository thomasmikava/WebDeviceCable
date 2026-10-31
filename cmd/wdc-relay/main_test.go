package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func clearRelayEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{"WDC_ADMIN_KEY", "WDC_ADMIN_KEY_FILE", "WDC_PROJECT_KEY", "WDC_PROJECT_KEY_FILE", "WDC_APP_ID", "WDC_APP_NAME", "WDC_APP_ORIGIN"} {
		t.Setenv(name, "")
	}
}

func TestRelayUsesOnlyOperatorConfiguration(t *testing.T) {
	clearRelayEnvironment(t)
	key := strings.Repeat("a", 64)
	t.Setenv("WDC_ADMIN_KEY", key)
	directory := t.TempDir()
	cfg, err := configFromEnvironment(directory)
	if err != nil || cfg.AdminKey != key || cfg.Directory != directory {
		t.Fatalf("operator configuration failed: %v", err)
	}
	for _, name := range []string{"WDC_PROJECT_KEY", "WDC_PROJECT_KEY_FILE", "WDC_APP_ID", "WDC_APP_NAME", "WDC_APP_ORIGIN"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(name, "obsolete-value")
			if _, err := configFromEnvironment(directory); err == nil || !strings.Contains(err.Error(), "register projects") || strings.Contains(err.Error(), "obsolete-value") {
				t.Fatalf("obsolete project configuration was not rejected safely: %v", err)
			}
		})
	}
}

func TestMountedAdminSecretTakesPrecedenceAndFailsClosed(t *testing.T) {
	clearRelayEnvironment(t)
	t.Setenv("WDC_ADMIN_KEY", strings.Repeat("a", 64))
	key := strings.Repeat("b", 64)
	directory := t.TempDir()
	file := filepath.Join(directory, "admin.key")
	if err := os.WriteFile(file, []byte(key+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WDC_ADMIN_KEY_FILE", file)
	cfg, err := configFromEnvironment(directory)
	if err != nil || cfg.AdminKey != key {
		t.Fatalf("mounted admin secret not used: %v", err)
	}
	if err = os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = configFromEnvironment(directory); err == nil {
		t.Fatal("empty secret silently fell back to environment")
	}
	t.Setenv("WDC_ADMIN_KEY_FILE", filepath.Join(directory, "missing.key"))
	if _, err = configFromEnvironment(directory); err == nil {
		t.Fatal("missing secret silently fell back to environment")
	}
}
