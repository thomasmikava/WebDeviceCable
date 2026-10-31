package bridge

import (
	"bytes"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type testPeer struct {
	key      *ecdsa.PrivateKey
	pin      string
	deviceID string
}
type testChannel struct {
	id      string
	channel *secureChannel
	peer    *testPeer
	origin  string
}

func (f *fixture) req(method, path, origin, token, lease string, body any, admin bool) *httptest.ResponseRecorder {
	if !strings.HasPrefix(path, "/v2/") || path == "/v2/channel" || path == "/v2/request" {
		return f.rawReq(method, path, origin, token, lease, body, admin)
	}
	f.clientsMu.Lock()
	peer := f.clients[token]
	if peer == nil {
		key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		peer = &testPeer{key: key, pin: publicKey(f.a.identity), deviceID: f.a.state.DeviceID}
		f.clients[token] = peer
	}
	f.clientsMu.Unlock()
	authorization := ""
	pairID := token
	if path == "/v2/pairings/claim" {
		pairID = ""
		raw, e := b64.DecodeString(strings.TrimPrefix(token, "aib2."))
		var invite struct {
			Secret    string `json:"secret"`
			PublicKey string `json:"publicKey"`
		}
		if e == nil && json.Unmarshal(raw, &invite) == nil {
			authorization = invite.Secret
			peer.pin = invite.PublicKey
		} else {
			authorization = token
		}
	}
	channel, w, e := f.testChannel(origin, peer)
	if e != nil {
		return testError(e)
	}
	if w != nil {
		return w
	}
	text := ""
	if body != nil {
		data, _ := json.Marshal(body)
		text = string(data)
	}
	request := secureRequest{Version: 2, Kind: "ai-bridge-request", SessionID: channel.id, Nonce: secret(), Timestamp: f.now.UnixMilli(), Origin: origin, PairingID: pairID, PublicKey: publicKey(peer.key), Method: method, Path: path, Authorization: authorization, Lease: lease, Body: text}
	envelope, e := testSeal(channel, request)
	if e != nil {
		return testError(e)
	}
	wire := f.rawReq("POST", "/v2/request", origin, "", "", envelope, false)
	if wire.Code != 200 {
		return wire
	}
	result, e := testDecode(channel, request, wire)
	if e != nil {
		return testError(e)
	}
	if path == "/v2/pairings/claim" && result.Code == 201 {
		var out struct {
			Pairing Pairing `json:"pairing"`
		}
		if json.Unmarshal(result.Body.Bytes(), &out) == nil {
			f.clientsMu.Lock()
			f.clients[out.Pairing.ID] = peer
			f.clientsMu.Unlock()
		}
	}
	return result
}
func testError(e error) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	writeJSON(w, 502, map[string]any{"error": e.Error()})
	return w
}
func (f *fixture) testChannel(origin string, peer *testPeer) (*testChannel, *httptest.ResponseRecorder, error) {
	ephemeral, e := ecdh.P256().GenerateKey(rand.Reader)
	if e != nil {
		return nil, nil, e
	}
	nonce := secret()
	clientKey := b64.EncodeToString(ephemeral.PublicKey().Bytes())
	w := f.rawReq("POST", "/v2/channel", origin, "", "", map[string]string{"nonce": nonce, "publicKey": clientKey}, false)
	if w.Code != 200 {
		return nil, w, nil
	}
	var signed signedMessage
	if e = json.Unmarshal(w.Body.Bytes(), &signed); e != nil {
		return nil, nil, e
	}
	data, e := verifyBytes(peer.pin, signed)
	if e != nil {
		return nil, nil, e
	}
	var hello channelHello
	if e = json.Unmarshal(data, &hello); e != nil {
		return nil, nil, e
	}
	if hello.Nonce != nonce || hello.ClientPublicKey != clientKey || hello.DeviceID != peer.deviceID || hello.Origin != origin || hello.Endpoint != f.origin {
		return nil, nil, errors.New("handshake binding mismatch")
	}
	raw, e := b64.DecodeString(hello.ServerPublicKey)
	if e != nil {
		return nil, nil, e
	}
	server, e := ecdh.P256().NewPublicKey(raw)
	if e != nil {
		return nil, nil, e
	}
	shared, e := ephemeral.ECDH(server)
	if e != nil {
		return nil, nil, e
	}
	salt, _ := b64.DecodeString(hello.Salt)
	send, e := channelCipher(shared, salt, hello.SessionID, "request")
	if e != nil {
		return nil, nil, e
	}
	receive, e := channelCipher(shared, salt, hello.SessionID, "response")
	if e != nil {
		return nil, nil, e
	}
	return &testChannel{id: hello.SessionID, peer: peer, origin: origin, channel: &secureChannel{Send: send, Receive: receive}}, nil, nil
}
func testSeal(channel *testChannel, request secureRequest) (encryptedMessage, error) {
	raw, e := json.Marshal(request)
	if e != nil {
		return encryptedMessage{}, e
	}
	signed, e := signBytes(channel.peer.key, raw)
	if e != nil {
		return encryptedMessage{}, e
	}
	encoded, _ := json.Marshal(signed)
	return sealMessage(channel.channel.Send, channel.id, "request", encoded)
}
func testFrames(channel *testChannel, request secureRequest, w *httptest.ResponseRecorder) ([]secureFrame, error) {
	messages := []encryptedMessage{}
	if strings.HasPrefix(w.Header().Get("Content-Type"), "text/event-stream") {
		for _, line := range strings.Split(w.Body.String(), "\n") {
			if strings.HasPrefix(line, "data: ") {
				var msg encryptedMessage
				if e := json.Unmarshal([]byte(line[6:]), &msg); e != nil {
					return nil, e
				}
				messages = append(messages, msg)
			}
		}
	} else {
		var msg encryptedMessage
		if e := json.Unmarshal(w.Body.Bytes(), &msg); e != nil {
			return nil, e
		}
		messages = append(messages, msg)
	}
	frames := []secureFrame{}
	previous := ""
	requestRaw, _ := json.Marshal(request)
	for _, message := range messages {
		raw, e := openMessage(channel.channel.Receive, message, "response")
		if e != nil {
			return nil, e
		}
		var signed signedMessage
		if e = json.Unmarshal(raw, &signed); e != nil {
			return nil, e
		}
		payload, e := verifyBytes(channel.peer.pin, signed)
		if e != nil {
			return nil, e
		}
		var frame secureFrame
		if e = json.Unmarshal(payload, &frame); e != nil {
			return nil, e
		}
		if frame.SessionID != request.SessionID || frame.Nonce != request.Nonce || frame.RequestHash != digest(string(requestRaw)) || frame.Path != request.Path || frame.Method != request.Method || frame.PairingID != request.PairingID || frame.Origin != request.Origin || frame.Sequence != uint64(len(frames)) || frame.Previous != previous {
			return nil, errors.New("response chain/binding mismatch")
		}
		previous = digest(string(payload))
		frames = append(frames, frame)
	}
	return frames, nil
}
func testDecode(channel *testChannel, request secureRequest, w *httptest.ResponseRecorder) (*httptest.ResponseRecorder, error) {
	frames, e := testFrames(channel, request, w)
	if e != nil {
		return nil, e
	}
	if len(frames) == 0 {
		return nil, errors.New("missing signed frame")
	}
	out := httptest.NewRecorder()
	if frames[0].Kind == "response" {
		out.WriteHeader(frames[0].Status)
		out.WriteString(frames[0].Body)
	} else {
		out.Header().Set("Content-Type", "text/event-stream")
		for _, frame := range frames {
			if frame.Body != "" {
				out.WriteString("event: job\ndata: " + frame.Body + "\n\n")
			}
		}
		if frames[len(frames)-1].Kind != "end" {
			return nil, errors.New("stream missing signed end")
		}
	}
	for k, v := range w.Header() {
		if k != "Content-Type" {
			out.Header()[k] = v
		}
	}
	return out, nil
}
func newPeer(t *testing.T, f *fixture) *testPeer {
	t.Helper()
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	return &testPeer{key: key, pin: publicKey(f.a.identity), deviceID: f.a.state.DeviceID}
}
func newTestRequest(f *fixture, channel *testChannel, path string) secureRequest {
	return secureRequest{Version: 2, Kind: "ai-bridge-request", SessionID: channel.id, Nonce: secret(), Timestamp: f.now.UnixMilli(), Origin: channel.origin, PublicKey: publicKey(channel.peer.key), Method: "GET", Path: path}
}
func TestUnsignedTransportAndControlTunnelsAreRejected(t *testing.T) {
	f := setup(t)
	for _, path := range []string{"/v1/capabilities", "/v2/capabilities", "/v2/jobs/x/events", "/v2/pairings/claim"} {
		w := f.rawReq("GET", path, f.origin, "stolen", "", nil, false)
		if w.Code != 426 {
			t.Fatalf("unsigned route allowed: %s %d", path, w.Code)
		}
	}
	peer := newPeer(t, f)
	channel, w, e := f.testChannel(f.origin, peer)
	if e != nil || w != nil {
		t.Fatal(e, w)
	}
	request := newTestRequest(f, channel, "/api/runner")
	request.Method = "POST"
	request.Body = `{"enabled":false}`
	envelope, _ := testSeal(channel, request)
	object(t, f.rawReq("POST", "/v2/request", f.origin, "", "", envelope, false), 400)
	if !f.a.state.Enabled {
		t.Fatal("control privilege escaped")
	}
}
func TestSecureRequestsRejectReplayModificationAndWrongKeys(t *testing.T) {
	f := setup(t)
	origin := "https://writer.example"
	token, _ := f.pair(t, "writer", origin, "alice", true)
	peer := f.clients[token]
	channel, w, e := f.testChannel(origin, peer)
	if e != nil || w != nil {
		t.Fatal(e, w)
	}
	request := newTestRequest(f, channel, "/v2/lease")
	request.PairingID = token
	request.Method = "POST"
	envelope, _ := testSeal(channel, request)
	first := f.rawReq("POST", "/v2/request", origin, "", "", envelope, false)
	if first.Code != 200 {
		t.Fatal(first.Code, first.Body)
	}
	frames, e := testFrames(channel, request, first)
	if e != nil || frames[0].Status != 201 {
		t.Fatal(e, frames)
	}
	object(t, f.rawReq("POST", "/v2/request", origin, "", "", envelope, false), 409)
	altered := envelope
	bytes, _ := b64.DecodeString(altered.Data)
	bytes[len(bytes)-1] ^= 1
	altered.Data = b64.EncodeToString(bytes)
	object(t, f.rawReq("POST", "/v2/request", origin, "", "", altered, false), 400)
	request.Nonce = secret()
	request.Timestamp = f.now.Add(-2 * time.Minute).UnixMilli()
	envelope, _ = testSeal(channel, request)
	object(t, f.rawReq("POST", "/v2/request", origin, "", "", envelope, false), 401)
	request.Timestamp = f.now.UnixMilli()
	request.Nonce = secret()
	raw, _ := json.Marshal(request)
	signed, _ := signBytes(peer.key, raw)
	request.Path = "/v2/jobs"
	modified, _ := json.Marshal(request)
	signed.Payload = b64.EncodeToString(modified)
	wire, _ := json.Marshal(signed)
	envelope, _ = sealMessage(channel.channel.Send, channel.id, "request", wire)
	object(t, f.rawReq("POST", "/v2/request", origin, "", "", envelope, false), 401)
	// A valid signature from a different app key cannot use this pairing ID.
	attacker := newPeer(t, f)
	request.Path = "/v2/capabilities"
	request.Method = "GET"
	request.PublicKey = publicKey(attacker.key)
	request.Nonce = secret()
	raw, _ = json.Marshal(request)
	signed, _ = signBytes(attacker.key, raw)
	wire, _ = json.Marshal(signed)
	envelope, _ = sealMessage(channel.channel.Send, channel.id, "request", wire)
	response := f.rawReq("POST", "/v2/request", origin, "", "", envelope, false)
	frames, e = testFrames(channel, request, response)
	if e != nil || frames[0].Status != 401 {
		t.Fatal(e, frames)
	}
}
func TestSignedFramesBindChunksAndAuthenticateCompletion(t *testing.T) {
	f := setup(t)
	peer := newPeer(t, f)
	channel, w, e := f.testChannel(f.origin, peer)
	if e != nil || w != nil {
		t.Fatal(e, w)
	}
	request := newTestRequest(f, channel, "/v2/jobs/example/events")
	target := httptest.NewRecorder()
	cancelled := false
	writer := &secureWriter{target: target, header: http.Header{"Content-Type": []string{"text/event-stream"}}, status: 200, app: f.a, channel: &secureChannel{Send: channel.channel.Receive}, request: request, requestHash: "", stream: true, cancel: func() { cancelled = true }}
	// Writer needs server-direction cipher; client's receive key is the same key.
	raw, _ := json.Marshal(request)
	writer.requestHash = digest(string(raw))
	for i, status := range []string{"running", "running", "completed"} {
		body, _ := json.Marshal(Job{ID: "example", Revision: uint64(i + 1), Status: status, Output: strings.Repeat("chunk ", i+1)})
		fmt.Fprintf(writer, "event: job\ndata: %s\n\n", body)
		writer.Flush()
	}
	if e := writer.finish(); e != nil {
		t.Fatal(e)
	}
	frames, e := testFrames(channel, request, target)
	if e != nil {
		t.Fatal(e)
	}
	if len(frames) != 3 || frames[0].Kind != "event" || frames[2].Kind != "end" || cancelled {
		t.Fatalf("bad signed stream %+v", frames)
	}
	var envelope encryptedMessage
	line := strings.Split(target.Body.String(), "\n")[1]
	json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &envelope)
	encrypted, _ := b64.DecodeString(envelope.Data)
	encrypted[0] ^= 1
	envelope.Data = b64.EncodeToString(encrypted)
	if _, e = openMessage(channel.channel.Receive, envelope, "response"); e == nil {
		t.Fatal("modified chunk accepted")
	}
}
func TestIdentityPersistenceAndResetInvalidatesPins(t *testing.T) {
	f := setup(t)
	token, pairID := f.pair(t, "writer", "https://writer.example", "alice", false)
	pin := publicKey(f.a.identity)
	f.a.Close()
	a, e := Open(f.a.dir)
	if e != nil {
		t.Fatal(e)
	}
	if publicKey(a.identity) != pin || a.state.Pairings[pairID].Revoked {
		t.Fatal("identity did not persist")
	}
	a.Close()
	if e = os.Remove(filepath.Join(f.a.dir, "identity.key")); e != nil {
		t.Fatal(e)
	}
	a, e = Open(f.a.dir)
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	if publicKey(a.identity) == pin || !a.state.Pairings[token].Revoked || !a.state.SecurityReset {
		t.Fatal("identity reset did not revoke prior trust")
	}
	info, _ := os.Stat(filepath.Join(f.a.dir, "identity.key"))
	// Windows protects these files with a DACL, checked by the Windows storage
	// tests. Its emulated Unix mode bits do not describe that access control.
	if runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		t.Fatal("signing key exposed by file mode")
	}
	state, _ := os.ReadFile(filepath.Join(f.a.dir, "state.json"))
	private, _ := os.ReadFile(filepath.Join(f.a.dir, "identity.key"))
	if bytes.Contains(state, private) {
		t.Fatal("private identity in state")
	}
}

func TestLegacyPairingsAreRevokedAndCorruptIdentityIsPreserved(t *testing.T) {
	f := setup(t)
	_, id := f.pair(t, "writer", "https://writer.example", "alice", false)
	p := f.a.state.Pairings[id]
	p.ClientPublicKey = ""
	p.CredentialHash = digest(secret())
	if e := f.a.save(&f.a.state); e != nil {
		t.Fatal(e)
	}
	f.a.Close()
	a, e := Open(f.a.dir)
	if e != nil {
		t.Fatal(e)
	}
	if !a.state.Pairings[id].Revoked || a.state.Pairings[id].CredentialHash != "" || !a.state.SecurityReset {
		t.Fatal("legacy bearer pairing was not invalidated")
	}
	a.Close()
	path := filepath.Join(f.a.dir, "identity.key")
	if e = os.WriteFile(path, []byte("corrupt key"), 0600); e != nil {
		t.Fatal(e)
	}
	if unexpected, e := Open(f.a.dir); e == nil {
		unexpected.Close()
		t.Fatal("corrupt identity was silently replaced")
	}
	data, e := os.ReadFile(path)
	if e != nil || string(data) != "corrupt key" {
		t.Fatal("corrupt identity was not preserved")
	}
}
