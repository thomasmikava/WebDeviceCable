package relay

import (
	"bytes"
	"crypto/elliptic"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPairingOwnershipReplayAndDurableReceipts(t *testing.T) {
	config := Config{Directory: t.TempDir(), AdminKey: strings.Repeat("a", 64)}
	s, err := Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	project := (registryHarness{t, s}).create(config.AdminKey, "test-project")
	request := func(method, path, key string, in any) (int, []byte) {
		t.Helper()
		b, _ := json.Marshal(in)
		r := httptest.NewRequest(method, path, bytes.NewReader(b))
		if key != "" {
			r.Header.Set("Authorization", "Bearer "+key)
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w.Code, w.Body.Bytes()
	}
	enroll := func(account string) (ticket, string) {
		t.Helper()
		status, b := request("POST", "/relay/v1/enrollments", project.APIKey, map[string]string{"appUser": account})
		if status != 201 {
			t.Fatalf("%d %s", status, b)
		}
		var result struct{ Code, ConnectionID string }
		_ = json.Unmarshal(b, &result)
		var ticket ticket
		if s.get("ticket", hash(result.Code), &ticket) != nil {
			t.Fatal("ticket missing")
		}
		return ticket, result.Code
	}
	a, code := enroll("admin-a")
	other, _ := enroll("admin-b")
	status, _ := request("POST", "/relay/v1/enrollments", "wrong", map[string]string{"appUser": "admin-a"})
	if status != 401 {
		t.Fatal("project authentication")
	}
	key, _ := newPrivate()
	client, _ := makeV2Client(&connection{PrivateKey: key}, nil, nil)
	inv := invitation{V: 2, Secret: ID(), Endpoint: "http://127.0.0.1:47321", DeviceID: ID(), PublicKey: b64.EncodeToString(elliptic.Marshal(client.key.Curve, client.key.X, client.key.Y)), AppID: project.Project.AppID, AppUser: "wrong-account", Origin: project.Project.Origin, ExpiresAt: time.Now().Add(time.Minute).UnixMilli()}
	claim := func() int {
		raw, _ := json.Marshal(inv)
		status, _ := request("POST", "/connector/v1/claim", "", map[string]string{"code": code, "invitation": "aib2." + b64.EncodeToString(raw), "token": strings.Repeat("t", 64)})
		return status
	}
	if claim() != 400 {
		t.Fatal("cross-account invitation accepted")
	}
	inv.AppUser = "admin-a"
	if claim() != 201 || claim() != 410 {
		t.Fatal("single-use code")
	}
	if _, err = Open(config); err == nil {
		t.Fatal("multiple processes acquired one database")
	}
	op := &operation{packet: Packet{ID: ID(), Deadline: time.Now().Add(time.Minute).UnixMilli()}, connection: a.ConnectionID, claimed: true, results: make(chan Result, 2), done: make(chan struct{})}
	s.operations[op.packet.ID] = op
	s.connections[other.ConnectionID] = &connection{ID: other.ConnectionID, ProjectID: other.ProjectID, AppUser: "admin-b", TokenHash: hash(strings.Repeat("u", 64))}
	result := Result{ID: op.packet.ID, Sequence: 0, Body: "encrypted-fixture", End: true}
	status, _ = request("POST", "/connector/v1/"+other.ConnectionID+"/result", strings.Repeat("u", 64), result)
	if status != 409 {
		t.Fatal("cross-account output accepted")
	}
	status, _ = request("POST", "/connector/v1/"+a.ConnectionID+"/result", strings.Repeat("t", 64), result)
	if status != 200 {
		t.Fatal("matching output rejected")
	}
	status, _ = request("POST", "/connector/v1/"+a.ConnectionID+"/result", strings.Repeat("t", 64), result)
	if status != 409 {
		t.Fatal("duplicate output accepted")
	}
	in := logicalRequest{ID: ID(), Deadline: time.Now().Add(45 * time.Second).UnixMilli(), Method: "POST", Path: "/v2/lease"}
	if s.reserve("receipt", hash(a.ConnectionID+":"+in.ID), "hash-only") != nil {
		t.Fatal("receipt write")
	}
	_ = s.Close()
	s, err = Open(config)
	if err != nil {
		t.Fatal(err)
	}
	status, _ = request("POST", "/relay/v1/connections/"+a.ConnectionID+"/request", project.APIKey, in)
	if status != 409 {
		t.Fatal("restart forgot consumed operation")
	}
	in.Deadline = time.Now().Add(-time.Second).UnixMilli()
	status, _ = request("POST", "/relay/v1/connections/"+a.ConnectionID+"/request", project.APIKey, in)
	if status != 408 {
		t.Fatal("stale request envelope replayed")
	}
	r := httptest.NewRequest("GET", "/healthz", nil)
	r.Header.Set("Origin", project.Project.Origin)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("browser-origin relay access")
	}
}
func TestEncryptedFramesRequirePinnedSignatureAndBinding(t *testing.T) {
	raw, _ := newPrivate()
	other, _ := newPrivate()
	a, _ := makeV2Client(&connection{PrivateKey: raw}, nil, nil)
	b, _ := makeV2Client(&connection{PrivateKey: other}, nil, nil)
	public := b64.EncodeToString(elliptic.Marshal(a.key.Curve, a.key.X, a.key.Y))
	signed, err := sign(a.key, []byte("fixture frame"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = verify(public, signed); err != nil {
		t.Fatal(err)
	}
	signed.Payload = b64.EncodeToString([]byte("altered frame"))
	if _, err = verify(public, signed); err == nil {
		t.Fatal("altered output verified")
	}
	signed, _ = sign(b.key, []byte("fixture frame"))
	if _, err = verify(public, signed); err == nil {
		t.Fatal("unpinned signer verified")
	}
	cipher, _ := derive([]byte("secret"), []byte("salt"), "connection-a", "response")
	envelope, _ := seal(cipher, "connection-a", "response", []byte("fixture"))
	if _, err = unseal(cipher, "connection-b", "response", envelope); err == nil {
		t.Fatal("cross-connection ciphertext verified")
	}
	if _, err = unseal(cipher, "connection-a", "request", envelope); err == nil {
		t.Fatal("cross-operation direction verified")
	}
}
