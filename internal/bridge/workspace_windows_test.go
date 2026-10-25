//go:build windows

package bridge

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestWorkspaceRejectsWindowsJunctionEscape(t *testing.T) {
	f, token, _, job := makeToolJob(t, "workspace")
	folder, outside := t.TempDir(), t.TempDir()
	path := filepath.Join(outside, "note.txt")
	if err := os.WriteFile(path, []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("cmd", "/c", "mklink", "/J", filepath.Join(folder, "escape"), outside).CombinedOutput(); err != nil {
		t.Fatalf("create directory junction: %v: %s", err, output)
	}
	object(t, f.req("PUT", "/api/pairings/"+token+"/permissions", f.origin, "", "", map[string]any{"folders": []FolderGrant{{Path: folder, Write: true, Automatic: true}}}, true), 200)
	f.a.state.Jobs[0].Status = "running"
	grant := f.a.state.Pairings[token].Folders[0]
	for _, name := range []string{"wdc_list", "wdc_read", "wdc_write"} {
		relative := "escape/note.txt"
		if name == "wdc_list" {
			relative = "escape"
		}
		args, _ := json.Marshal(map[string]any{"folder": grant.ID, "path": relative, "text": "must stay private"})
		if _, err := f.a.invokeNativeTool(context.Background(), job, name, args); err == nil {
			t.Fatalf("%s escaped through a junction", name)
		}
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "outside" {
		t.Fatal("outside file changed", string(data), err)
	}
}
