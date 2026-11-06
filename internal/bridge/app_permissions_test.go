package bridge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestAppPermissionCheckboxesSaveTogether(t *testing.T) {
	f := setupConfig(t, "bridge:\n  disableNative: true\n")
	origin := "https://permissions.example"
	pair, _ := f.pair(t, "Permissions app", origin, "alice", false)
	other, _ := f.pair(t, "Other app", origin, "bob", false)
	lease := f.lease(t, origin, pair)
	pending := object(t, f.req("POST", "/v2/permissions", origin, pair, lease, map[string]bool{"files": true}, false), 202)
	path := "/api/pairings/" + pair
	folder := t.TempDir()
	changes := map[string]any{"automatic": true, "files": true, "folders": []FolderGrant{{Path: folder, Write: true}}}
	object(t, f.req("PATCH", path, f.origin, "", "", changes, false), 401)
	object(t, f.req("PATCH", path, f.origin, "", "", map[string]any{"automatic": true, "files": true, "displayName": "invalid\nname"}, true), 400)
	object(t, f.req("PATCH", path, f.origin, "", "", map[string]any{"automatic": true, "files": true, "folders": []FolderGrant{{Path: f.a.dir}}}, true), 400)
	if f.a.state.Pairings[pair].Automatic || f.a.state.Pairings[pair].Files {
		t.Fatal("rejected permission save partially granted access")
	}
	object(t, f.req("PATCH", path, f.origin, "", "", changes, true), 200)
	if !f.a.state.Pairings[pair].Automatic || !f.a.state.Pairings[pair].Files || f.a.state.Pairings[other].Automatic || f.a.state.Pairings[other].Files {
		t.Fatal("permission changes were lost or crossed app scopes")
	}
	grants := f.a.state.Pairings[pair].Folders
	if len(grants) != 1 || grants[0].ID == "" || grants[0].Identity == "" || !grants[0].Write || len(f.a.state.Pairings[other].Folders) != 0 {
		t.Fatal("folder permissions were lost, unvalidated or crossed app scopes")
	}
	request := object(t, f.req("GET", "/v2/permissions/"+pending["id"].(string), origin, pair, lease, nil, false), 200)
	if request["status"] != "completed" {
		t.Fatal("saving file access did not resolve its pending request")
	}
	data, err := os.ReadFile(filepath.Join(f.a.dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var saved State
	if json.Unmarshal(data, &saved) != nil || !saved.Pairings[pair].Automatic || !saved.Pairings[pair].Files || len(saved.Pairings[pair].Folders) != 1 {
		t.Fatal("permission checkboxes were not persisted together")
	}
}

func TestFailedAppPermissionSaveKeepsGrantsAndFileWork(t *testing.T) {
	f, pair, lease, job := makeToolJob(t, "app-tools")
	grantFiles(t, f, pair)
	object(t, f.req("PATCH", "/api/pairings/"+pair, f.origin, "", "", map[string]any{"folders": []FolderGrant{{Path: t.TempDir()}}}, true), 200)
	input := uploadForTest(t, f, "https://tools.example", pair, lease, "/v2/inputs", []byte("original file"), "text/plain")
	findJob(&f.a.state, job).UsesFiles = true
	revision := f.a.state.Pairings[pair].PermissionsRevision
	saveID := secret()
	// Force the actual transaction's atomic rename to fail in disposable state.
	path := filepath.Join(f.a.dir, "state.json")
	previous := path + ".before-test"
	if err := os.Rename(path, previous); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Remove(path); err != nil {
			t.Error(err)
		}
		if err := os.Rename(previous, path); err != nil {
			t.Error(err)
		}
	})
	object(t, f.req("PATCH", "/api/pairings/"+pair, f.origin, "", "", map[string]any{"automatic": false, "files": false, "folders": []FolderGrant{}, "expectedPermissionsRevision": revision, "permissionSaveId": saveID}, true), 500)
	if !f.a.state.Pairings[pair].Automatic || !f.a.state.Pairings[pair].Files || len(f.a.state.Pairings[pair].Folders) != 1 || findJob(&f.a.state, job).Status != "running" || f.a.inputs[input] == nil {
		t.Fatal("failed save partially revoked permissions or interrupted file work")
	}
	if f.a.state.Pairings[pair].PermissionSaveID != "" || f.a.state.Pairings[pair].PermissionsRevision != revision {
		t.Fatal("failed persistence published a receipt or permission revision")
	}
}

func TestPermissionDraftRejectsRevokedFolderFromAnotherWindow(t *testing.T) {
	f := setupConfig(t, "bridge:\n  disableNative: true\n")
	pair, _ := f.pair(t, "Draft", "https://draft.example", "owner", false)
	path := "/api/pairings/" + pair
	object(t, f.req("PATCH", path, f.origin, "", "", map[string]any{"folders": []FolderGrant{{Path: t.TempDir()}, {Path: t.TempDir()}}}, true), 200)
	p := f.a.state.Pairings[pair]
	base := p.PermissionsRevision
	draft := append([]FolderGrant(nil), p.Folders...)
	retained := draft[1]
	object(t, f.req("PUT", path+"/permissions", f.origin, "", "", map[string]any{"folders": []FolderGrant{retained}}, true), 200)
	draft[1].Write = true
	response := object(t, f.req("PATCH", path, f.origin, "", "", map[string]any{"expectedPermissionsRevision": base, "permissionSaveId": secret(), "automatic": true, "files": true, "folders": draft}, true), 409)
	if response["error"].(map[string]any)["code"] != "permissions_changed" {
		t.Fatal(response)
	}
	p = f.a.state.Pairings[pair]
	if len(p.Folders) != 1 || p.Folders[0].ID != retained.ID || p.Folders[0].Write || p.Automatic || p.Files || p.PermissionSaveID != "" {
		t.Fatal("stale draft changed or restored revoked access")
	}
}

func TestPermissionRevisionTracksAllWritersAndPreventsABA(t *testing.T) {
	f := setupConfig(t, "bridge:\n  disableNative: true\n")
	pair, _ := f.pair(t, "Revision", "https://revision.example", "owner", false)
	path := "/api/pairings/" + pair
	base := f.a.state.Pairings[pair].PermissionsRevision
	object(t, f.req("PATCH", path, f.origin, "", "", map[string]string{"displayName": "Local name"}, true), 200)
	if f.a.state.Pairings[pair].PermissionsRevision != base {
		t.Fatal("appearance edit invalidated permission drafts")
	}
	grantFiles(t, f, pair)
	object(t, f.req("PUT", path+"/files", f.origin, "", "", map[string]bool{"allowed": false}, true), 200)
	if f.a.state.Pairings[pair].PermissionsRevision != base+2 {
		t.Fatal("grant and revocation did not each advance the permission revision")
	}
	object(t, f.req("PATCH", path, f.origin, "", "", map[string]any{"expectedPermissionsRevision": base, "automatic": true}, true), 409)
	// Other app/job metadata does not cause false permission conflicts.
	if err := f.a.update(func(s *State) error { s.Pairings[pair].Routing.Revision++; return nil }); err != nil {
		t.Fatal(err)
	}
	if f.a.state.Pairings[pair].PermissionsRevision != base+2 {
		t.Fatal("routing change invalidated permission draft")
	}
}

func TestPermissionSaveReceiptRecoversWithoutReapplying(t *testing.T) {
	f := setupConfig(t, "bridge:\n  disableNative: true\n")
	origin := "https://receipt.example"
	pair, _ := f.pair(t, "Receipt", origin, "owner", false)
	path := "/api/pairings/" + pair
	object(t, f.req("GET", path, f.origin, "", "", nil, false), 401)
	base := f.a.state.Pairings[pair].PermissionsRevision
	id := secret()
	folder := t.TempDir()
	request := map[string]any{"expectedPermissionsRevision": base, "permissionSaveId": id, "files": true, "automatic": true, "folders": []FolderGrant{{Path: folder}}}
	object(t, f.req("PATCH", path, f.origin, "", "", request, true), 200)
	revision := f.a.state.Revision
	p := f.a.state.Pairings[pair]
	if p.PermissionSaveID != id || p.PermissionSaveHash == "" || p.PermissionsRevision != base+1 {
		t.Fatal("permission receipt or revision was not published")
	}
	// Recover after the path itself disappears; the original mutation was accepted.
	if err := os.Remove(folder); err != nil {
		t.Fatal(err)
	}
	object(t, f.req("PATCH", path, f.origin, "", "", request, true), 200)
	if f.a.state.Revision != revision {
		t.Fatal("receipt retry reapplied a permission mutation")
	}
	changed := map[string]any{}
	for key, value := range request {
		changed[key] = value
	}
	changed["files"] = false
	object(t, f.req("PATCH", path, f.origin, "", "", changed, true), 409)
	owner := object(t, f.req("GET", path, f.origin, "", "", nil, true), 200)
	if owner["permissionSaveId"] != id || owner["permissionSaveHash"] != nil {
		t.Fatal("owner receipt missing or its private hash leaked")
	}
	remote := object(t, f.req("GET", "/v2/capabilities", origin, pair, "", nil, false), 200)["pairing"].(map[string]any)
	if remote["permissionSaveId"] != nil || remote["permissionsRevision"] != nil {
		t.Fatal("owner-only save metadata leaked to the app")
	}
	data, err := os.ReadFile(filepath.Join(f.a.dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var stored State
	if json.Unmarshal(data, &stored) != nil || stored.Pairings[pair].PermissionSaveID != id || stored.Pairings[pair].PermissionSaveHash == "" {
		t.Fatal("receipt was not durably stored")
	}
	object(t, f.req("PUT", path+"/files", f.origin, "", "", map[string]bool{"allowed": false}, true), 200)
	object(t, f.req("PATCH", path, f.origin, "", "", request, true), 200)
	if f.a.state.Pairings[pair].Files {
		t.Fatal("retry restored access revoked after the accepted save")
	}
	if err := f.a.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(f.a.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if reopened.state.Pairings[pair].PermissionSaveID != id || reopened.state.Pairings[pair].PermissionSaveHash == "" || reopened.state.Pairings[pair].Files {
		t.Fatal("restart lost the accepted save receipt or restored revoked access")
	}
}
