package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestUpdateReplacementPreparesAndPreservesRecovery(t *testing.T) {
	root := t.TempDir()
	staged := filepath.Join(root, "staged")
	installed := filepath.Join(root, "installed")
	backup := filepath.Join(root, "private-backup")
	for _, dir := range []string{staged, installed, backup} {
		if e := os.Mkdir(dir, 0700); e != nil {
			t.Fatal(e)
		}
	}
	for name, body := range map[string][]byte{"WebDeviceCable": {0, 1, 2, 255}, "WebDeviceCable Launcher.exe": {3, 4, 5, 128}} {
		if e := os.WriteFile(filepath.Join(installed, name), []byte("previous"), 0700); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(filepath.Join(staged, name), body, 0700); e != nil {
			t.Fatal(e)
		}
	}
	replacements, e := prepareReplacements(staged, installed, backup, []string{"WebDeviceCable", "WebDeviceCable Launcher.exe"})
	if e != nil {
		t.Fatal(e)
	}
	before, _ := os.ReadFile(filepath.Join(installed, "WebDeviceCable"))
	if string(before) != "previous" {
		t.Fatal("preparation changed the live installation")
	}
	for _, r := range replacements {
		if e = replaceUpdateFile(r.next, r.target); e != nil {
			t.Fatal(e)
		}
		old, _ := os.ReadFile(r.backup)
		if string(old) != "previous" {
			t.Fatal("lost recovery bytes")
		}
	}
	after, _ := os.ReadFile(filepath.Join(installed, "WebDeviceCable"))
	if !bytes.Equal(after, []byte{0, 1, 2, 255}) {
		t.Fatal("replacement corrupted bytes")
	}
	for _, r := range replacements {
		if e = restoreReplacement(r); e != nil {
			t.Fatal(e)
		}
		b, _ := os.ReadFile(r.target)
		if string(b) != "previous" {
			t.Fatal("failed to restore original executable")
		}
	}
}
func TestFailedUpdatePreparationLeavesLiveInstallationUntouched(t *testing.T) {
	root := t.TempDir()
	staged := filepath.Join(root, "stage")
	installed := filepath.Join(root, "installed")
	backup := filepath.Join(root, "backup")
	for _, dir := range []string{staged, installed, backup} {
		_ = os.Mkdir(dir, 0700)
	}
	_ = os.WriteFile(filepath.Join(installed, "WebDeviceCable"), []byte("keep"), 0700)
	_ = os.WriteFile(filepath.Join(staged, "WebDeviceCable"), []byte("replace"), 0700)
	if _, e := prepareReplacements(staged, installed, backup, []string{"WebDeviceCable", "missing"}); e == nil {
		t.Fatal("accepted missing payload")
	}
	body, _ := os.ReadFile(filepath.Join(installed, "WebDeviceCable"))
	if string(body) != "keep" {
		t.Fatal("failed preparation changed live executable")
	}
	files, _ := filepath.Glob(filepath.Join(installed, ".WebDeviceCable-update-*"))
	if len(files) != 0 {
		t.Fatal("left incomplete replacement files")
	}
}
