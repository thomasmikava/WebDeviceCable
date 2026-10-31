package relay

import (
	"bytes"
	"context"
	"crypto/elliptic"
	"database/sql"
	"encoding/json"
	"io"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type registryHarness struct {
	t *testing.T
	s *Server
}

func (h registryHarness) request(method, path, key string, input any, headers map[string]string) *httptest.ResponseRecorder {
	h.t.Helper()
	b, _ := json.Marshal(input)
	r := httptest.NewRequest(method, path, bytes.NewReader(b))
	if key != "" {
		r.Header.Set("Authorization", "Bearer "+key)
	}
	for name, value := range headers {
		r.Header.Set(name, value)
	}
	w := httptest.NewRecorder()
	h.s.Handler().ServeHTTP(w, r)
	return w
}

func expectStatus(t *testing.T, w *httptest.ResponseRecorder, status int) {
	t.Helper()
	if w.Code != status {
		t.Fatalf("wanted %d, got %d: %s", status, w.Code, w.Body.String())
	}
}

type projectCredential struct {
	Project projectView `json:"project"`
	APIKey  string      `json:"apiKey"`
}

func decodeCredential(t *testing.T, w *httptest.ResponseRecorder) projectCredential {
	t.Helper()
	var value projectCredential
	if err := json.Unmarshal(w.Body.Bytes(), &value); err != nil || len(value.APIKey) < 32 || len(value.Project.ID) != 64 {
		t.Fatalf("invalid project credential: %s (%v)", w.Body.String(), err)
	}
	return value
}

func (h registryHarness) create(admin, appID string) projectCredential {
	h.t.Helper()
	w := h.request("POST", "/relay/admin/v1/projects", admin, map[string]string{"appId": appID, "name": appID, "origin": "https://" + appID + ".example"}, nil)
	expectStatus(h.t, w, 201)
	return decodeCredential(h.t, w)
}

func (h registryHarness) enroll(key string) (ticket, string) {
	h.t.Helper()
	w := h.request("POST", "/relay/v1/enrollments", key, map[string]string{"appUser": "same-account"}, nil)
	expectStatus(h.t, w, 201)
	var out struct{ Code string }
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	var ticket ticket
	if err := h.s.get("ticket", hash(out.Code), &ticket); err != nil {
		h.t.Fatal(err)
	}
	return ticket, out.Code
}

func (h registryHarness) claim(code, appID, origin, token string) *httptest.ResponseRecorder {
	h.t.Helper()
	key, _ := newPrivate()
	client, _ := makeV2Client(&connection{PrivateKey: key}, nil, nil)
	inv := invitation{V: 2, Secret: ID(), Endpoint: "http://127.0.0.1:47321", DeviceID: ID(), PublicKey: b64.EncodeToString(elliptic.Marshal(client.key.Curve, client.key.X, client.key.Y)), AppID: appID, AppUser: "same-account", Origin: origin, ExpiresAt: time.Now().Add(time.Minute).UnixMilli()}
	raw, _ := json.Marshal(inv)
	return h.request("POST", "/connector/v1/claim", "", map[string]string{"code": code, "invitation": "aib2." + b64.EncodeToString(raw), "token": token}, nil)
}

func TestDynamicProjectsIsolationRotationRevocationAndRestart(t *testing.T) {
	config := Config{Directory: t.TempDir(), AdminKey: strings.Repeat("a", 64)}
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	h := registryHarness{t, s}
	root := "/relay/admin/v1/projects"
	expectStatus(t, h.request("GET", root, "", nil, nil), 401)
	expectStatus(t, h.request("GET", root, config.AdminKey, nil, map[string]string{"Origin": "https://operator.example"}), 403)
	a, b := h.create(config.AdminKey, "alpha"), h.create(config.AdminKey, "beta")
	expectStatus(t, h.request("GET", root, a.APIKey, nil, nil), 401)
	expectStatus(t, h.request("POST", "/relay/v1/enrollments", config.AdminKey, map[string]string{"appUser": "same-account"}, nil), 401)
	expectStatus(t, h.request("POST", root, config.AdminKey, map[string]string{"appId": "alpha", "name": "Alpha", "origin": "https://alpha.example"}, nil), 409)
	list := h.request("GET", root, config.AdminKey, nil, nil)
	expectStatus(t, list, 200)
	if strings.Contains(list.Body.String(), "apiKey") || strings.Contains(list.Body.String(), "credentialHash") || strings.Contains(list.Body.String(), a.APIKey) {
		t.Fatal("management reads disclose credentials")
	}
	for _, identity := range []map[string]string{
		{"appId": "invalid", "name": "Invalid", "origin": "http://remote.example"},
		{"appId": "invalid", "name": "Invalid", "origin": "https://example.com/path"},
		{"appId": "invalid", "name": "Invalid", "origin": "https://example.com?"},
		{"appId": "invalid", "name": "Invalid", "origin": "https://example.com:99999"},
		{"appId": strings.Repeat("x", 101), "name": "Invalid", "origin": "https://example.com"},
	} {
		expectStatus(t, h.request("POST", root, config.AdminKey, identity, nil), 400)
	}
	ta, codeA := h.enroll(a.APIKey)
	tb, codeB := h.enroll(b.APIKey)
	if ta.ProjectID == tb.ProjectID {
		t.Fatal("enrollments not scoped to projects")
	}
	inspect := h.request("POST", "/connector/v1/inspect", "", map[string]string{"code": codeA}, nil)
	expectStatus(t, inspect, 200)
	if !strings.Contains(inspect.Body.String(), `"appId":"alpha"`) {
		t.Fatal("wrong desktop consent identity")
	}
	expectStatus(t, h.claim(codeA, b.Project.AppID, b.Project.Origin, strings.Repeat("t", 64)), 400)
	expectStatus(t, h.claim(codeA, a.Project.AppID, b.Project.Origin, strings.Repeat("t", 64)), 400)
	expectStatus(t, h.claim(codeA, a.Project.AppID, a.Project.Origin, strings.Repeat("t", 64)), 201)
	expectStatus(t, h.claim(codeB, b.Project.AppID, b.Project.Origin, strings.Repeat("u", 64)), 201)
	pathA := "/relay/v1/connections/" + ta.ConnectionID
	expectStatus(t, h.request("GET", pathA, b.APIKey, nil, nil), 404)
	expectStatus(t, h.request("DELETE", pathA, b.APIKey, nil, nil), 404)
	expectStatus(t, h.request("GET", pathA, a.APIKey, nil, map[string]string{"X-WDC-Account": "other-account"}), 403)
	logical := logicalRequest{ID: ID(), Deadline: time.Now().Add(45 * time.Second).UnixMilli(), Method: "GET", Path: "/v2/capabilities"}
	expectStatus(t, h.request("POST", pathA+"/request", b.APIKey, logical, nil), 403)
	for _, path := range []string{"/v1/chat/completions", "/v1/responses", "/v1/messages", "/v1beta/models/wdc-default:generateContent", "/v1beta/models/wdc-default:streamGenerateContent?alt=sse"} {
		body := map[string]any{"model": "wdc-default", "input": "hello"}
		if strings.Contains(path, "chat/completions") || path == "/v1/messages" {
			body = map[string]any{"model": "wdc-default", "messages": []map[string]string{{"role": "user", "content": "hello"}}}
		}
		if strings.Contains(path, "/v1beta/") {
			body = map[string]any{"contents": []map[string]any{{"role": "user", "parts": []map[string]string{{"text": "hello"}}}}}
		}
		expectStatus(t, h.request("POST", path, b.APIKey, body, map[string]string{"X-WDC-Connection": ta.ConnectionID, "Idempotency-Key": "isolation-fixture"}), 403)
	}
	pending, pendingCode := h.enroll(a.APIKey)
	pathPending := "/relay/v1/connections/" + pending.ConnectionID
	expectStatus(t, h.request("DELETE", pathPending, b.APIKey, nil, nil), 404)
	expectStatus(t, h.request("POST", "/connector/v1/inspect", "", map[string]string{"code": pendingCode}, nil), 200)
	rotated := h.request("POST", root+"/"+a.Project.ID+"/rotate-key", config.AdminKey, nil, nil)
	expectStatus(t, rotated, 200)
	newA := decodeCredential(t, rotated)
	expectStatus(t, h.request("GET", pathA, a.APIKey, nil, nil), 401)
	expectStatus(t, h.request("GET", pathA, newA.APIKey, nil, nil), 200)
	expectStatus(t, h.request("DELETE", pathPending, newA.APIKey, nil, nil), 200)
	expectStatus(t, h.claim(pendingCode, a.Project.AppID, a.Project.Origin, ID()), 410)
	// Persisted credential rotation and ownership survive an operator restart.
	_ = s.Close()
	s, err = Open(config)
	if err != nil {
		t.Fatal(err)
	}
	h.s = s
	expectStatus(t, h.request("GET", pathA, a.APIKey, nil, nil), 401)
	expectStatus(t, h.request("GET", pathA, newA.APIKey, nil, nil), 200)
	_, unclaimed := h.enroll(newA.APIKey)
	s.mu.Lock()
	c := s.connections[ta.ConnectionID]
	s.lastSeen[c.ID] = time.Now()
	s.mu.Unlock()
	response, err := s.tunnel(context.Background(), c, "/v2/request", []byte("encrypted-fixture"))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	s.mu.Lock()
	for _, op := range s.operations {
		op.claimed = true
	}
	s.mu.Unlock()
	pollDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		pollDone <- h.request("POST", "/connector/v1/"+c.ID+"/poll", strings.Repeat("t", 64), nil, nil)
	}()
	expectStatus(t, h.request("DELETE", root+"/"+a.Project.ID, config.AdminKey, nil, nil), 200)
	if _, err = io.ReadAll(response.Body); err == nil {
		t.Fatal("project revocation did not cancel active tunnel")
	}
	select {
	case w := <-pollDone:
		expectStatus(t, w, 401)
	case <-time.After(time.Second):
		t.Fatal("revoked connector long poll stayed alive")
	}
	expectStatus(t, h.request("POST", "/connector/v1/inspect", "", map[string]string{"code": unclaimed}, nil), 410)
	expectStatus(t, h.request("GET", pathA, newA.APIKey, nil, nil), 401)
	expectStatus(t, h.request("GET", "/relay/v1/connections/"+tb.ConnectionID, b.APIKey, nil, nil), 200)
	expectStatus(t, h.request("DELETE", root+"/"+a.Project.ID, config.AdminKey, nil, nil), 200)
	expectStatus(t, h.request("POST", root+"/"+a.Project.ID+"/rotate-key", config.AdminKey, nil, nil), 404)
	expectStatus(t, h.request("POST", root, config.AdminKey, map[string]string{"appId": "alpha", "name": "Alpha", "origin": "https://alpha.example"}, nil), 409)
	_ = s.Close()
	s, err = Open(config)
	if err != nil {
		t.Fatal(err)
	}
	h.s = s
	expectStatus(t, h.request("POST", "/connector/v1/"+ta.ConnectionID+"/poll", strings.Repeat("t", 64), nil, nil), 401)
	expectStatus(t, h.request("GET", "/relay/v1/connections/"+tb.ConnectionID, b.APIKey, nil, nil), 200)
	var raw string
	if err = s.db.QueryRow("SELECT group_concat(data) FROM records WHERE kind='project'").Scan(&raw); err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{config.AdminKey, a.APIKey, b.APIKey, newA.APIKey} {
		if strings.Contains(raw, secret) {
			t.Fatal("registry persisted a plaintext credential")
		}
	}
}

func TestStoredProjectsLoadWithoutIdentityConfiguration(t *testing.T) {
	cfg := Config{Directory: t.TempDir(), AdminKey: strings.Repeat("a", 64)}
	s, err := Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	h := registryHarness{t, s}
	project := h.create(cfg.AdminKey, "existing")
	paired, code := h.enroll(project.APIKey)
	expectStatus(t, h.claim(code, project.Project.AppID, project.Project.Origin, ID()), 201)
	_, pendingCode := h.enroll(project.APIKey)
	// Older registry entries may contain obsolete configuration metadata. It
	// must never override a stored project credential or alter its identity.
	var record map[string]any
	if err = s.get("project", project.Project.ID, &record); err != nil {
		t.Fatal(err)
	}
	record["bootstrapKeyHash"] = hash("retired-configuration-secret")
	if err = s.put("project", project.Project.ID, record); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	cfg.AdminKey = strings.Repeat("b", 64)
	s, err = Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	h.s = s
	path := "/relay/v1/connections/" + paired.ConnectionID
	expectStatus(t, h.request("GET", path, project.APIKey, nil, nil), 200)
	expectStatus(t, h.request("POST", "/connector/v1/inspect", "", map[string]string{"code": pendingCode}, nil), 200)
	rotation := h.request("POST", "/relay/admin/v1/projects/"+project.Project.ID+"/rotate-key", cfg.AdminKey, nil, nil)
	expectStatus(t, rotation, 200)
	rotated := decodeCredential(t, rotation)
	_ = s.Close()
	s, err = Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	h.s = s
	expectStatus(t, h.request("GET", path, project.APIKey, nil, nil), 401)
	expectStatus(t, h.request("GET", path, rotated.APIKey, nil, nil), 200)
	h.enroll(rotated.APIKey)
}

func TestUnscopedStateIsRejectedWithoutMutation(t *testing.T) {
	for _, kind := range []string{"connection", "ticket"} {
		t.Run(kind, func(t *testing.T) {
			cfg := Config{Directory: t.TempDir(), AdminKey: strings.Repeat("a", 64)}
			s, err := Open(cfg)
			if err != nil {
				t.Fatal(err)
			}
			project := (registryHarness{t, s}).create(cfg.AdminKey, "existing")
			id := ID()
			var record any = &connection{ID: id, AppUser: "owner", Identity: invitation{AppID: project.Project.AppID, Origin: project.Project.Origin}}
			if kind == "ticket" {
				record = ticket{AppUser: "owner", CodeHash: id, ConnectionID: ID(), Expires: time.Now().Add(time.Minute)}
			}
			if err = s.put(kind, id, record); err != nil {
				t.Fatal(err)
			}
			expected, _ := json.Marshal(record)
			_ = s.Close()
			if bad, err := Open(cfg); err == nil {
				bad.Close()
				t.Fatal("unscoped state was adopted")
			} else if !strings.Contains(err.Error(), "unscoped relay") {
				t.Fatal(err)
			}
			db, err := sql.Open("sqlite", filepath.Join(cfg.Directory, "relay.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			var actual string
			if err = db.QueryRow("SELECT data FROM records WHERE kind=? AND id=?", kind, id).Scan(&actual); err != nil || actual != string(expected) {
				t.Fatalf("failed startup changed existing state: %v", err)
			}
			var projects int
			if err = db.QueryRow("SELECT count(*) FROM records WHERE kind='project'").Scan(&projects); err != nil || projects != 1 {
				t.Fatalf("failed startup changed projects: %v", err)
			}
		})
	}
}

func TestProjectConfigurationValidation(t *testing.T) {
	for _, key := range []string{"", "short", strings.Repeat("a", 31)} {
		if s, err := Open(Config{Directory: t.TempDir(), AdminKey: key}); err == nil {
			s.Close()
			t.Fatal("missing or short admin key accepted")
		}
	}
	if !validIdentity("test", "Test", "http://[::1]:8080") {
		t.Fatal("explicit IPv6 loopback rejected")
	}
	cfg := Config{Directory: t.TempDir(), AdminKey: strings.Repeat("a", 64)}
	s, err := Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	h := registryHarness{t, s}
	list := h.request("GET", "/relay/admin/v1/projects", cfg.AdminKey, nil, nil)
	expectStatus(t, list, 200)
	if strings.TrimSpace(list.Body.String()) != `{"projects":[]}` {
		t.Fatal("startup implicitly created a project")
	}
	project := h.create(cfg.AdminKey, "test")
	h.enroll(project.APIKey)
}
