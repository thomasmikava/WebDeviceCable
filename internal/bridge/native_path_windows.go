package bridge

import (
	"os"
	"path/filepath"
	"sort"
	"time"
)

func nativePlatformPaths(name string) []string {
	if name != "codex" {
		return nil
	}
	local := os.Getenv("LOCALAPPDATA")
	if !filepath.IsAbs(local) {
		return nil
	}
	// The Codex desktop app keeps its native CLI in a versioned runtime cache.
	// GUI launches do not inherit the PATH additions made by the Codex app.
	root := filepath.Join(local, "OpenAI", "Codex", "bin")
	paths := []string{filepath.Join(root, "codex.exe")}
	entries, _ := os.ReadDir(root)
	type runtimePath struct {
		path     string
		modified time.Time
	}
	var runtimes []runtimePath
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(root, entry.Name(), "codex.exe")
		if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
			continue // Other components and incomplete downloads share this cache.
		}
		if info, err := entry.Info(); err == nil {
			runtimes = append(runtimes, runtimePath{path, info.ModTime()})
		}
	}
	sort.Slice(runtimes, func(i, j int) bool {
		if runtimes[i].modified.Equal(runtimes[j].modified) {
			return runtimes[i].path < runtimes[j].path
		}
		return runtimes[i].modified.After(runtimes[j].modified)
	})
	for _, runtime := range runtimes {
		paths = append(paths, runtime.path)
	}
	return paths
}
