package bridge

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNativePathFindsWindowsCodexDesktopWithoutPATH(t *testing.T) {
	local := t.TempDir()
	t.Setenv("LOCALAPPDATA", local)
	t.Setenv("APPDATA", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	root := filepath.Join(local, "OpenAI", "Codex", "bin")
	write := func(version string) string {
		t.Helper()
		path := filepath.Join(root, version, "codex.exe")
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	old := write("old")
	latest := write("latest")
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(filepath.Dir(old), past, past); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "incomplete"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "directory", "codex.exe"), 0700); err != nil {
		t.Fatal(err)
	}
	check := func(want string) {
		t.Helper()
		got, err := nativePath("codex")
		if err != nil || got != want {
			t.Fatalf("got %q, %v; want %q", got, err, want)
		}
	}
	check(latest)
	if err := os.Remove(latest); err != nil {
		t.Fatal(err)
	}
	check(old)
	stable := write("")
	check(stable)
	if err := os.Remove(stable); err != nil {
		t.Fatal(err)
	}
	cli := filepath.Join(t.TempDir(), "codex.exe")
	if err := os.WriteFile(cli, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Dir(cli))
	check(cli) // A deliberately installed CLI on PATH still takes precedence.
	t.Setenv("PATH", t.TempDir())
	explicit := AIProgram{Type: "codex", Executable: cli}
	if got, err := explicit.path(); err != nil || got != cli {
		t.Fatalf("explicit executable lost precedence: %q, %v", got, err)
	}
	if _, err := nativePath("claude"); err == nil {
		t.Fatal("Codex desktop was used for a different integration")
	}
	t.Setenv("LOCALAPPDATA", "")
	if _, err := nativePath("codex"); err == nil {
		t.Fatal("found Codex without a configured installation root")
	}
	t.Setenv("LOCALAPPDATA", "relative")
	if paths := nativePlatformPaths("codex"); len(paths) != 0 {
		t.Fatalf("searched a relative installation root: %v", paths)
	}
}

func TestNativePathFindsWindowsUserBinWithoutPATH(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("LOCALAPPDATA", t.TempDir())
	t.Setenv("APPDATA", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	bin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(bin, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"codex", "claude"} {
		path := filepath.Join(bin, name+".exe")
		if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
		if got, err := nativePath(name); err != nil || got != path {
			t.Fatalf("%s: got %q, %v; want %q", name, got, err, path)
		}
	}
}

func TestNativePathFindsWindowsClaudeDesktopWithoutPATH(t *testing.T) {
	for _, packaged := range []bool{false, true} {
		name := "desktop"
		if packaged {
			name = "store"
		}
		t.Run(name, func(t *testing.T) {
			local, roaming, home := t.TempDir(), t.TempDir(), t.TempDir()
			t.Setenv("LOCALAPPDATA", local)
			t.Setenv("APPDATA", roaming)
			t.Setenv("USERPROFILE", home)
			t.Setenv("PATH", t.TempDir())
			root := filepath.Join(roaming, "Claude", "claude-code")
			if packaged {
				root = filepath.Join(local, "Packages", "Claude_fixture", "LocalCache", "Roaming", "Claude", "claude-code")
			}
			write := func(path string) {
				t.Helper()
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			old := filepath.Join(root, "2.1.310", "old", "claude.exe")
			latest := filepath.Join(root, "2.1.317", "latest", "claude.exe")
			write(old)
			write(latest)
			past := time.Now().Add(-time.Hour)
			if err := os.Chtimes(filepath.Dir(old), past, past); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(root, "2.1.318", "incomplete", "claude.exe"), 0700); err != nil {
				t.Fatal(err)
			}
			check := func(want string) {
				t.Helper()
				if got, err := nativePath("claude"); err != nil || got != want {
					t.Fatalf("got %q, %v; want %q", got, err, want)
				}
			}
			check(latest)
			if err := os.Remove(latest); err != nil {
				t.Fatal(err)
			}
			check(old)
			cli := filepath.Join(home, ".local", "bin", "claude.exe")
			write(cli)
			check(cli) // Keep a standalone CLI preferred over a desktop cache.
			pathCLI := filepath.Join(t.TempDir(), "claude.exe")
			write(pathCLI)
			t.Setenv("PATH", filepath.Dir(pathCLI))
			check(pathCLI)
		})
	}
}
