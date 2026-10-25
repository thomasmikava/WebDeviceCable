//go:build !windows

package bridge

import "os"

func hardenExistingStorage(string) error { return nil }

func replaceLocalFile(from, to string) error { return os.Rename(from, to) }

func privatePermissions(path string, directory bool) error {
	mode := os.FileMode(0600)
	if directory {
		mode = 0700
	}
	return os.Chmod(path, mode)
}
func syncDirectory(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
