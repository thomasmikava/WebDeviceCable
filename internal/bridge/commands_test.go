package bridge

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
)

func TestCommandConfigurationFileBoundaries(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if e := os.WriteFile(path, []byte(starterConfig), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := readPrivate(path); e != nil {
		t.Fatal(e)
	}
	if runtime.GOOS != "windows" {
		if e := os.Chmod(path, 0666); e != nil {
			t.Fatal(e)
		}
		if _, e := readPrivate(path); e == nil {
			t.Fatal("accepted a configuration writable by another user")
		}
		if e := os.Chmod(path, 0600); e != nil {
			t.Fatal(e)
		}
		link := filepath.Join(dir, "link.yaml")
		if e := os.Symlink(path, link); e != nil {
			t.Fatal(e)
		}
		if _, e := readPrivate(link); e == nil {
			t.Fatal("accepted a configuration symlink")
		}
		if e := writePrivate(link, []byte(starterConfig)); e == nil {
			t.Fatal("replaced a configuration symlink")
		}
	}
	if _, e := readPrivate(dir); e == nil {
		t.Fatal("accepted a directory as configuration")
	}
	if e := os.WriteFile(path, make([]byte, (128<<10)+1), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := readPrivate(path); e == nil {
		t.Fatal("accepted an oversized configuration")
	}
}

func helperAction(t *testing.T, id, operation string) Action {
	t.Helper()
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	return Action{ID: id, Title: id, Exec: []string{exe, "-test.run=^TestCommandHelper$", "--", operation}, Timeout: 5}
}
func commandFixture(t *testing.T, editable bool, actions ...Action) *fixture {
	t.Helper()
	cfg := Config{Actions: actions}
	cfg.Bridge.AllowWebCommandEditing = editable
	data, e := yaml.Marshal(cfg)
	if e != nil {
		t.Fatal(e)
	}
	return setupConfig(t, string(data))
}
func grantCommands(t *testing.T, f *fixture, pairID string, ids ...string) {
	t.Helper()
	grants := map[string]string{}
	for _, id := range ids {
		grants[id] = f.a.actions[id].Hash
	}
	object(t, f.req("PATCH", "/api/pairings/"+pairID, f.origin, "", "", map[string]any{"actions": grants, "commandPermissionsAccepted": true}, true), 200)
}
func waitJob(t *testing.T, f *fixture, id string, condition func(*Job) bool) *Job {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		f.tick(t)
		j := findJob(&f.a.state, id)
		if condition(j) {
			return j
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("job did not reach expected state: %+v", findJob(&f.a.state, id))
	return nil
}
func TestCommandHelper(t *testing.T) {
	index := -1
	for i, v := range os.Args {
		if v == "--" {
			index = i
			break
		}
	}
	if index < 0 {
		return
	}
	args := os.Args[index+1:]
	if len(args) == 0 {
		os.Exit(9)
	}
	switch args[0] {
	case "file-roundtrip":
		var files commandFiles
		if json.Unmarshal([]byte(os.Getenv("WDC_FILES")), &files) != nil || len(files.Inputs) != 1 {
			os.Exit(10)
		}
		data, err := os.ReadFile(files.Inputs[0].Path)
		if err != nil {
			os.Exit(11)
		}
		if err = os.WriteFile(filepath.Join(files.OutputDirectory, "result.bin"), data, 0600); err != nil {
			os.Exit(12)
		}
		fmt.Print("File returned unchanged.")
	case "echo":
		data, _ := json.Marshal(args[1:])
		fmt.Print(string(data))
		if os.Getenv("BRIDGE_TEST_SECRET") != "" {
			fmt.Print("LEAKED_ENVIRONMENT")
		}
	case "stream":
		fmt.Print("first\n")
		time.Sleep(350 * time.Millisecond)
		fmt.Fprint(os.Stderr, "second\n")
		time.Sleep(350 * time.Millisecond)
		fmt.Print("last\n")
	case "sleep":
		fmt.Print("started\n")
		time.Sleep(30 * time.Second)
	case "fail":
		fmt.Fprint(os.Stderr, "configured failure")
		os.Exit(7)
	case "flood":
		fmt.Print(strings.Repeat("\x00\x1b\"\\\n", 10000))
	default:
		os.Exit(8)
	}
	os.Exit(0)
}
func TestOliveTinExecConfigValidation(t *testing.T) {
	good := `bridge:
  allowWebCommandEditing: false
actions:
  - id: greet
    title: Say hello
    exec: [my-tool, "{{ .Arguments.message }}", "{{ count }}"]
    timeout: 30
    maxConcurrent: 2
    arguments:
      - name: message
        type: ascii_sentence
        default: Hello
      - name: count
        type: int
        default: 3
`
	cfg, e := parseConfig([]byte(good))
	if e != nil {
		t.Fatal(e)
	}
	a := cfg.Actions[0]
	values, e := a.inputs(nil)
	if e != nil {
		t.Fatal(e)
	}
	argv := a.argv(values)
	if strings.Join(argv, "|") != "my-tool|Hello|3" {
		t.Fatal(argv)
	}
	for name, bad := range map[string]string{
		"unknown policy":       good + "unknown: true\n",
		"unknown action":       strings.Replace(good, "timeout: 30", "typoTimeout: 30", 1),
		"shell":                strings.Replace(good, `exec: [my-tool, "{{ .Arguments.message }}", "{{ count }}"]`, `shell: my-tool`, 1),
		"executable input":     strings.Replace(good, "my-tool", `"{{ count }}"`, 1),
		"environment template": strings.Replace(good, "{{ count }}", "{{ .Env.SECRET }}", 1),
		"unknown input":        strings.Replace(good, "{{ count }}", "{{ absent }}", 1),
		"unknown type":         strings.Replace(good, "type: int", "type: html", 1),
		"native override":      strings.Replace(good, "id: greet", "id: codex", 1),
		"native executable":    strings.Replace(good, "my-tool", "codex", 1),
		"second document":      good + "---\nactions: []\n",
		"duplicate key":        good + "actions: []\n",
		"aliases":              "actions: &copy []\nbridge: *copy\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, e := parseConfig([]byte(bad)); e == nil {
				t.Fatal("invalid configuration accepted")
			}
		})
	}
	if _, e = a.inputs(map[string]string{"count": "-1"}); e == nil {
		t.Fatal("negative int accepted")
	}
	if _, e = a.inputs(map[string]string{"message": "hello; touch unwanted"}); e == nil {
		t.Fatal("typed input was not checked")
	}
	if _, e = a.inputs(map[string]string{"extra": "x"}); e == nil {
		t.Fatal("unknown input accepted")
	}
	choice := Action{Arguments: []Argument{{Name: "target", Choices: []Choice{{Value: "one"}, {Value: "two"}}}}}
	if _, e = choice.inputs(map[string]string{"target": "three"}); e == nil {
		t.Fatal("unconfigured choice accepted")
	}
}
func TestCommandsRequireExplicitVersionedWebsiteGrants(t *testing.T) {
	action := helperAction(t, "greet", "echo")
	action.Exec = append(action.Exec, "{{ .Arguments.message }}")
	action.Arguments = []Argument{{Name: "message", Type: "very_dangerous_raw_string", Default: "Hello"}}
	f := commandFixture(t, false, action)
	token, id := f.pair(t, "writer", f.origin, "alice", false)
	lease := f.lease(t, f.origin, token)
	request := map[string]any{"kind": "command", "action": "greet", "idempotencyKey": "command-no-grant"}
	object(t, f.req("POST", "/v2/jobs", f.origin, token, lease, request, false), 403)
	object(t, f.req("PATCH", "/api/pairings/"+id, f.origin, "", "", map[string]any{"actions": map[string]string{"greet": f.a.actions["greet"].Hash}}, true), 400)
	object(t, f.req("PATCH", "/api/pairings/"+id, f.origin, "", "", map[string]any{"actions": map[string]string{"greet": "stale"}, "commandPermissionsAccepted": true}, true), 409)
	grantCommands(t, f, id, "greet")
	t.Setenv("BRIDGE_TEST_SECRET", "never inherit this")
	message := "literal $(touch forbidden); & | > < \" '"
	request["arguments"] = map[string]string{"message": message}
	result := object(t, f.req("POST", "/v2/jobs", f.origin, token, lease, request, false), 202)
	jobID := result["id"].(string)
	f.tick(t)
	if len(f.a.commands) != 0 {
		t.Fatal("command started before local approval")
	}
	object(t, f.req("POST", "/api/jobs/"+jobID+"/decision", f.origin, "", "", map[string]bool{"approve": true}, true), 200)
	job := waitJob(t, f, jobID, func(j *Job) bool { return terminal(j.Status) })
	expected, _ := json.Marshal([]string{message})
	if job.Status != "completed" || job.Output != string(expected) || job.ExitCode == nil || *job.ExitCode != 0 {
		t.Fatalf("wrong command result: %+v", job)
	}
	if len(job.Request.Arguments) != 0 {
		t.Fatal("terminal inputs retained")
	}
	object(t, f.req("POST", "/v2/jobs", f.origin, token, lease, request, false), 200)
	bad := map[string]any{"kind": "command", "action": "greet", "mode": "restricted", "idempotencyKey": "command-wrong-mode"}
	object(t, f.req("POST", "/v2/jobs", f.origin, token, lease, bad, false), 400)
	caps := object(t, f.req("GET", "/v2/actions", f.origin, token, "", nil, false), 200)
	encoded, _ := json.Marshal(caps)
	if strings.Contains(string(encoded), "-test.run") || strings.Contains(string(encoded), "exec") {
		t.Fatal("executable leaked to website")
	}
	other, _ := f.pair(t, "other", f.origin, "bob", true)
	otherLease := f.lease(t, f.origin, other)
	object(t, f.req("POST", "/v2/jobs", f.origin, other, otherLease, request, false), 403)
}
func TestCommandProgressFailuresLimitsAndCancellation(t *testing.T) {
	stream := helperAction(t, "stream", "stream")
	fail := helperAction(t, "fail", "fail")
	flood := helperAction(t, "flood", "flood")
	sleep := helperAction(t, "sleep", "sleep")
	sleep.Timeout = 1
	f := commandFixture(t, false, stream, fail, flood, sleep)
	f.a.config.Bridge.MaxTextBytes = 16 << 10
	token, pairID := f.pair(t, "writer", f.origin, "alice", true)
	grantCommands(t, f, pairID, "stream", "fail", "flood", "sleep")
	lease := f.lease(t, f.origin, token)
	create := func(id, key string) string {
		return f.job(t, f.origin, token, lease, key, map[string]any{"kind": "command", "action": id})
	}
	id := create("stream", "stream-progress")
	progress := waitJob(t, f, id, func(j *Job) bool { return j.Status == "running" && strings.Contains(j.Output, "first") })
	if strings.Contains(progress.Output, "last") {
		t.Fatal("stream buffered to completion")
	}
	done := waitJob(t, f, id, func(j *Job) bool { return terminal(j.Status) })
	if done.Output != "first\nsecond\nlast\n" {
		t.Fatal(done.Output)
	}
	id = create("fail", "failed-command")
	done = waitJob(t, f, id, func(j *Job) bool { return terminal(j.Status) })
	if done.Status != "failed" || done.ExitCode == nil || *done.ExitCode != 7 || !strings.Contains(done.Output, "configured failure") {
		t.Fatal(done)
	}
	id = create("flood", "bounded-output")
	done = waitJob(t, f, id, func(j *Job) bool { return terminal(j.Status) })
	if !done.OutputTruncated || len(done.Output) > 16<<10 || strings.ContainsRune(done.Output, 0) {
		t.Fatal("output not bounded/cleaned")
	}
	object(t, f.req("GET", "/v2/jobs/"+id, f.origin, token, "", nil, false), 200)
	id = create("sleep", "real-timeout")
	done = waitJob(t, f, id, func(j *Job) bool { return terminal(j.Status) })
	if done.Status != "timed_out" {
		t.Fatal(done)
	}
	id = create("stream", "close-cancels-command")
	waitJob(t, f, id, func(j *Job) bool { return j.Status == "running" && j.Output != "" })
	run := f.a.commands[id]
	object(t, f.req("DELETE", "/v2/lease", f.origin, token, lease, nil, false), 200)
	select {
	case <-run.stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("closing tab did not stop process")
	}
	if findJob(&f.a.state, id).Status != "cancelled" {
		t.Fatal("job not cancelled")
	}
}
func TestWebCommandGateIsStartupOnlyAndNeverWritesPolicy(t *testing.T) {
	action := helperAction(t, "file-action", "echo")
	f := commandFixture(t, false, action)
	newAction := helperAction(t, "web-action", "stream")
	object(t, f.req("POST", "/api/commands", f.origin, "", "", newAction, true), 403)
	path := filepath.Join(f.a.dir, "config.yaml")
	data, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	data = []byte(strings.Replace(string(data), "allowWebCommandEditing: false", "allowWebCommandEditing: true", 1))
	if e = os.WriteFile(path, data, 0600); e != nil {
		t.Fatal(e)
	}
	object(t, f.req("POST", "/api/commands", f.origin, "", "", newAction, true), 403)
	// Reopen reads the file once. No browser/API route can modify this setting.
	f.a.Close()
	a, e := Open(f.a.dir)
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	if !a.config.Bridge.AllowWebCommandEditing {
		t.Fatal("startup gate was not loaded")
	}
	if e = a.editAction(action.ID, &action); e == nil {
		t.Fatal("file action was edited from web")
	}
	if e = a.editAction(newAction.ID, &newAction); e != nil {
		t.Fatal(e)
	}
	unchanged, _ := os.ReadFile(path)
	if string(unchanged) != string(data) {
		t.Fatal("web editor rewrote config.yaml")
	}
	a.Close()
	a, e = Open(f.a.dir)
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	if a.actions[newAction.ID].Source != "web" {
		t.Fatal("web command did not persist")
	}
}
func TestEditingCommandCancelsRunningJobsAndRequiresRenewedGrant(t *testing.T) {
	f := commandFixture(t, true)
	action := helperAction(t, "editable", "sleep")
	object(t, f.req("POST", "/api/commands", f.origin, "", "", action, true), 201)
	token, id := f.pair(t, "writer", f.origin, "alice", true)
	grantCommands(t, f, id, action.ID)
	lease := f.lease(t, f.origin, token)
	jobID := f.job(t, f.origin, token, lease, "before-command-edit", map[string]any{"kind": "command", "action": action.ID})
	waitJob(t, f, jobID, func(j *Job) bool { return j.Status == "running" && j.Output != "" })
	run := f.a.commands[jobID]
	action.Exec = helperAction(t, "editable", "echo").Exec
	object(t, f.req("PUT", "/api/commands/"+action.ID, f.origin, "", "", action, true), 200)
	select {
	case <-run.stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("edited command kept running")
	}
	object(t, f.req("POST", "/v2/jobs", f.origin, token, lease, map[string]any{"kind": "command", "action": action.ID, "idempotencyKey": "after-command-edit"}, false), 403)
	grantCommands(t, f, id, action.ID)
	jobID = f.job(t, f.origin, token, lease, "after-command-regrant", map[string]any{"kind": "command", "action": action.ID})
	job := waitJob(t, f, jobID, func(j *Job) bool { return terminal(j.Status) })
	if job.Status != "completed" {
		t.Fatal(job)
	}
	object(t, f.req("DELETE", "/api/commands/"+action.ID, f.origin, "", "", nil, true), 200)
}
