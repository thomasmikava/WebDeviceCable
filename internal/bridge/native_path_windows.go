package bridge

import (
	"os"
	"path/filepath"
	"sort"
	"time"
)

func nativePlatformPaths(name string) []string {
	local := os.Getenv("LOCALAPPDATA")
	if name == "claude" {
		paths := []string{}
		if home, err := os.UserHomeDir(); err == nil && filepath.IsAbs(home) {
			paths = append(paths, filepath.Join(home, ".local", "bin", "claude.exe"))
		}
		roots := []string{}
		if roaming := os.Getenv("APPDATA"); filepath.IsAbs(roaming) {
			roots = append(roots, filepath.Join(roaming, "Claude", "claude-code"))
		}
		if filepath.IsAbs(local) {
			// Store installations redirect the desktop app's roaming cache here.
			packaged, _ := filepath.Glob(filepath.Join(local, "Packages", "Claude_*", "LocalCache", "Roaming", "Claude", "claude-code"))
			roots = append(roots, packaged...)
		}
		candidates := []string{}
		for _, root := range roots {
			files, _ := filepath.Glob(filepath.Join(root, "*", "*", "claude.exe"))
			candidates = append(candidates, files...)
		}
		return append(paths, newestWindowsRuntimes(candidates)...)
	}
	if name != "codex" || !filepath.IsAbs(local) {
		return nil
	}
	// The Codex desktop app keeps its native CLI in a versioned runtime cache.
	// GUI launches do not inherit the PATH additions made by the Codex app.
	root := filepath.Join(local, "OpenAI", "Codex", "bin")
	paths := []string{filepath.Join(root, "codex.exe")}
	candidates, _ := filepath.Glob(filepath.Join(root, "*", "codex.exe"))
	return append(paths, newestWindowsRuntimes(candidates)...)
}

func newestWindowsRuntimes(candidates []string) []string {
	type runtimePath struct {
		path     string
		modified time.Time
	}
	var runtimes []runtimePath
	for _, path := range candidates {
		if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
			continue // Other components and incomplete downloads share this cache.
		}
		if info, err := os.Stat(filepath.Dir(path)); err == nil && info.IsDir() {
			runtimes = append(runtimes, runtimePath{path, info.ModTime()})
		}
	}
	sort.Slice(runtimes, func(i, j int) bool {
		if runtimes[i].modified.Equal(runtimes[j].modified) {
			return runtimes[i].path < runtimes[j].path
		}
		return runtimes[i].modified.After(runtimes[j].modified)
	})
	paths := []string{}
	for _, runtime := range runtimes {
		paths = append(paths, runtime.path)
	}
	return paths
}
