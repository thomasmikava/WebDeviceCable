//go:build !windows

package main

import (
	"errors"
	"os"
)

func verifyExecutable(path string) error {
	i, e := os.Lstat(path)
	if e != nil {
		return e
	}
	if !i.Mode().IsRegular() || i.Mode()&0111 == 0 {
		return errors.New("update executable is missing or unsafe")
	}
	return nil
}

func replaceUpdateFile(from, to string) error { return os.Rename(from, to) }
func installedVersion(string, string) error   { return nil }
