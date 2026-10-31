package relay

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type facadeFixture struct {
	mu          sync.Mutex
	jobs        map[string]string
	submissions int
}

func (f *facadeFixture) call(ctx context.Context, method, path, body, lease, auth string) (*http.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	result := any(map[string]any{})
	contentType := "application/json"
	switch {
	case path == "/v2/lease":
		result = map[string]string{"lease": ID(), "expiresAt": time.Now().Add(Grace).Format(time.RFC3339)}
	case path == "/v2/inputs":
		result = map[string]string{"id": ID()}
	case path == "/v2/jobs":
		var in struct {
			OutputSchema any
			Prompt       string
		}
		_ = json.Unmarshal([]byte(body), &in)
		id := ID()
		text := "fixture reply"
		if in.OutputSchema != nil {
			text = `{"ok":true}`
		}
		if strings.Contains(in.Prompt, "FAIL_STREAM") {
			text = "fixture-failure"
		}
		f.jobs[id] = text
		f.submissions++
		result = map[string]any{"id": id, "status": "running", "revision": 1}
	case strings.HasSuffix(path, "/events"):
		id := strings.Split(path, "/")[3]
		text := f.jobs[id]
		status := "completed"
		if text == "fixture-failure" {
			status = "failed"
			text = ""
		}
		b, _ := json.Marshal(map[string]any{"id": id, "status": status, "revision": 3, "output": text, "actualModel": "fixture-native", "usage": map[string]int{"inputTokens": 10, "outputTokens": 2, "totalTokens": 12}})
		contentType = "text/event-stream"
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{contentType}}, Body: io.NopCloser(strings.NewReader("event: job\ndata: " + string(b) + "\n\n"))}, nil
	}
	b, _ := json.Marshal(result)
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{contentType}}, Body: io.NopCloser(strings.NewReader(string(b)))}, nil
}
func TestProviderSDKContracts(t *testing.T) {
	root := filepath.Join("..", "..", "tests", "relay-contract")
	if _, e := os.Stat(filepath.Join(root, "node_modules", "openai")); e != nil {
		t.Skip("npm ci --prefix tests/relay-contract to run official SDK contracts")
	}
	config := Config{Directory: t.TempDir(), AdminKey: strings.Repeat("a", 64)}
	s, e := Open(config)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	credential := (registryHarness{t, s}).create(config.AdminKey, "contract")
	id := ID()
	fixture := &facadeFixture{jobs: map[string]string{}}
	s.connections[id] = &connection{ID: id, ProjectID: credential.Project.ID, AppUser: "contract-admin"}
	s.lastSeen[id] = time.Now()
	s.clients[id] = fixture
	server := httptest.NewServer(s.Handler())
	defer server.Close()
	cmd := exec.Command("node", filepath.Join(root, "contract.mjs"))
	cmd.Env = append(os.Environ(), "WDC_CONTRACT_URL="+server.URL, "WDC_CONTRACT_KEY="+credential.APIKey, "WDC_CONTRACT_CONNECTION="+id)
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("%v\n%s", e, out)
	}
	t.Log(string(out))
	if fixture.submissions != 15 {
		t.Fatalf("unsupported requests reached inference: %d", fixture.submissions)
	}
}
