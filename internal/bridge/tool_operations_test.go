package bridge

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestToolOperationDetailsAreScopedImmutableAndRetained(t *testing.T) {
	f, token, lease, job := makeToolJob(t, "app-tools")
	origin := "https://tools.example"
	f.a.mu.Lock()
	j := findJob(&f.a.state, job)
	j.Request.Tools = []AppTool{{ID: "execute_tool", Title: "Execute", InputSchema: json.RawMessage(`{"type":"object","additionalProperties":true}`)}}
	f.a.mu.Unlock()
	done := make(chan error, 1)
	go func() {
		_, err := f.a.invokeNativeTool(context.Background(), job, "app_"+digest("execute_tool")[:24], json.RawMessage(`{"operation":"rename"}`))
		done <- err
	}()
	var id string
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline) && id == ""; {
		f.a.mu.Lock()
		for key := range f.a.permissions {
			id = key
		}
		f.a.mu.Unlock()
		if id == "" {
			time.Sleep(time.Millisecond)
		}
	}
	if id == "" {
		t.Fatal("no callback")
	}
	path := "/v2/tool-calls/" + id
	details := map[string]any{"id": "rename_page", "title": "Renaming a page", "version": "catalog-1"}
	object(t, f.req("POST", path+"/operation", origin, token, lease, details, false), 409)
	object(t, f.req("POST", path+"/claim", origin, token, lease, nil, false), 200)
	other, _ := f.pair(t, "other", origin, "bob", true)
	otherLease := f.lease(t, origin, other)
	object(t, f.req("POST", path+"/operation", origin, other, otherLease, details, false), 404)
	object(t, f.req("POST", path+"/operation", origin, token, lease, details, false), 200)
	object(t, f.req("POST", path+"/operation", origin, token, lease, details, false), 200)
	object(t, f.req("POST", path+"/operation", origin, token, lease, map[string]string{"id": "delete_page", "title": "Different"}, false), 409)
	f.a.mu.Lock()
	snapshot := publicJob(findJob(&f.a.state, job))
	f.a.mu.Unlock()
	if len(snapshot.ToolOperations) != 1 || snapshot.ToolOperations[0].Status != "running" || snapshot.ToolOperations[0].ToolID != "execute_tool" {
		t.Fatal(snapshot.ToolOperations)
	}
	object(t, f.req("POST", path+"/result", origin, token, lease, map[string]any{"result": true}, false), 200)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	f.a.mu.Lock()
	snapshot = publicJob(findJob(&f.a.state, job))
	f.a.mu.Unlock()
	if snapshot.ToolOperations[0].Status != "returned" {
		t.Fatal(snapshot.ToolOperations)
	}
	data, err := ReadLocalFile(f.a.dir+"/state.json", 32<<20)
	if err != nil {
		t.Fatal(err)
	}
	var saved State
	if json.Unmarshal(data, &saved) != nil || findJob(&saved, job).ToolOperations[0].ID != "rename_page" {
		t.Fatal("details not persisted")
	}
	finish(&snapshot, "cancelled", "cancelled", "", time.Now())
	if snapshot.ToolOperations[0].Status != "returned" {
		t.Fatal("completed call changed")
	}
	snapshot.ToolOperations[0].Status = "running"
	finish(&snapshot, "cancelled", "cancelled", "", time.Now())
	if snapshot.ToolOperations[0].Status != "unconfirmed" {
		t.Fatal("uncertain outcome reported as success")
	}
}
