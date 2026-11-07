//go:build !windows

package bridge

func nativePlatformPaths(name string) []string { return nil }
