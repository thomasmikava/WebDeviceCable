//go:build !windows

package bridge

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestWorkspaceSpecialFilesDoNotBlockCancellation(t *testing.T) {
	f, token, _, job := makeToolJob(t, "workspace")
	folder := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(folder, "pipe"), 0600); err != nil {
		t.Fatal(err)
	}
	object(t, f.req("PUT", "/api/pairings/"+token+"/permissions", f.origin, "", "", map[string]any{"folders": []FolderGrant{{Path: folder}}}, true), 200)
	f.a.state.Jobs[0].Status = "running"
	grant := f.a.state.Pairings[token].Folders[0]
	args, _ := json.Marshal(map[string]string{"folder": grant.ID, "path": "pipe"})
	for _, tool := range []string{"wdc_read", "wdc_list"} {
		done := make(chan error, 1)
		go func() { _, err := f.a.invokeNativeTool(context.Background(), job, tool, args); done <- err }()
		select {
		case err := <-done:
			if err == nil {
				t.Fatal("special file accepted")
			}
		case <-time.After(time.Second):
			t.Fatal("special file blocked the dispatcher")
		}
	}
	// A path reused for a different directory must not inherit the previous grant.
	if err := os.Rename(folder, folder+"-old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(folder, 0700); err != nil {
		t.Fatal(err)
	}
	args, _ = json.Marshal(map[string]string{"folder": grant.ID, "path": "."})
	if _, err := f.a.invokeNativeTool(context.Background(), job, "wdc_list", args); err == nil {
		t.Fatal("replacement folder inherited access")
	}
	t.Cleanup(func() { os.RemoveAll(folder + "-old") })
}
