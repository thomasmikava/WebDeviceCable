package relay

import (
	"bytes"
	"context"
	"crypto/elliptic"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/thomasmikava/WebDeviceCable/internal/wirelimits"
)

func TestScopedCapabilitiesRequireProjectAuthenticationAndBoundCreation(t *testing.T) {
	config := Config{Directory: t.TempDir(), AdminKey: strings.Repeat("a", 64)}
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	h := registryHarness{t, s}
	project := h.create(config.AdminKey, "scoped-project")
	for _, key := range []string{"", config.AdminKey, "invalid"} {
		expectStatus(t, h.request("GET", "/relay/v1/capabilities", key, nil, nil), 401)
	}
	response := h.request("GET", "/relay/v1/capabilities", project.APIKey, nil, nil)
	expectStatus(t, response, 200)
	var caps struct {
		Version int            `json:"scopedToolsVersion"`
		Limits  map[string]int `json:"limits"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &caps); err != nil {
		t.Fatal(err)
	}
	if caps.Version != 1 || caps.Limits["toolDefinitionBytes"] != wirelimits.Definitions || caps.Limits["encryptedEnvelopeBytes"] != wirelimits.Envelope {
		t.Fatal("missing execution capabilities", response.Body.String())
	}
	if _, capped := caps.Limits["toolsPerExecution"]; capped {
		t.Fatal("relay still advertises a tool-count cap")
	}
	path := "/relay/v1/connections/" + ID() + "/request"
	logical := logicalRequest{ID: ID(), Deadline: time.Now().Add(45 * time.Second).UnixMilli(), Method: "POST", Path: "/v2/jobs", Body: `{"tools":"` + strings.Repeat("x", wirelimits.Definitions) + `"}`}
	response = h.request("POST", path, project.APIKey, logical, nil)
	expectStatus(t, response, 413)
	if !strings.Contains(response.Body.String(), "tool_set_too_large") {
		t.Fatal(response.Body.String())
	}
	logical.Path = "/v2/jobs/lookup"
	logical.Body = strings.Repeat("x", wirelimits.Ordinary+1)
	response = h.request("POST", path, project.APIKey, logical, nil)
	expectStatus(t, response, 413)
	if !strings.Contains(response.Body.String(), "request_too_large") {
		t.Fatal(response.Body.String())
	}
	logical.Body = strings.Repeat("x", wirelimits.Envelope)
	response = h.request("POST", path, project.APIKey, logical, nil)
	expectStatus(t, response, 413)
	if !strings.Contains(response.Body.String(), "request_too_large") {
		t.Fatal(response.Body.String())
	}
	expectStatus(t, h.request("POST", path, "", logical, nil), 401)
}

func TestAdmittedLogicalCatalogFitsSignedEncryptedEnvelope(t *testing.T) {
	for _, text := range []string{"<>&", "\u2028\u2029"} {
		// Exercise the real signing/encryption path at the logical definition
		// budget, including both HTML characters and JSON line separators.
		body := `{"tools":[{"description":"` + strings.Repeat(text, (wirelimits.Definitions-100)/len(text)) + `"}]}`
		if wirelimits.Check("POST", "/v2/jobs", body) != "" {
			t.Fatal("fixture exceeds the logical byte budget")
		}
		key, err := newPrivate()
		if err != nil {
			t.Fatal(err)
		}
		id := ID()
		aead, err := derive(bytes.Repeat([]byte{1}, 32), bytes.Repeat([]byte{2}, 32), id, "request")
		if err != nil {
			t.Fatal(err)
		}
		ch := &channel{id: id, send: aead, receive: aead}
		stop := errors.New("packet inspected")
		var client *v2Client
		client, err = makeV2Client(&connection{ID: ID(), PrivateKey: key, Identity: invitation{Origin: "https://large.example"}}, func(_ context.Context, path string, wire []byte) (*http.Response, error) {
			if path != "/v2/request" || len(wire) > wirelimits.Envelope {
				t.Fatalf("admitted logical catalog expanded to %d bytes", len(wire))
			}
			var envelope envelope
			if err := json.Unmarshal(wire, &envelope); err != nil {
				t.Fatal(err)
			}
			opened, err := unseal(aead, id, "request", envelope)
			if err != nil {
				t.Fatal(err)
			}
			var signature signed
			if err = json.Unmarshal(opened, &signature); err != nil {
				t.Fatal(err)
			}
			public := b64.EncodeToString(elliptic.Marshal(client.key.Curve, client.key.X, client.key.Y))
			verified, err := verify(public, signature)
			if err != nil {
				t.Fatal(err)
			}
			var request struct{ Body string }
			if json.Unmarshal(verified, &request) != nil || request.Body != body {
				t.Fatal("encoding changed the signed logical request")
			}
			return nil, stop
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = client.exchange(context.Background(), ch, ID(), "POST", "/v2/jobs", body, "lease", ""); !errors.Is(err, stop) {
			t.Fatal(err)
		}
	}
}
