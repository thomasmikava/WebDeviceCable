package bridge

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/thomasmikava/WebDeviceCable/internal/wirelimits"
)

const channelLifetime = 15 * time.Minute

var b64 = base64.RawURLEncoding

type signedMessage struct {
	Payload   string `json:"payload"`
	Signature string `json:"signature"`
}
type encryptedMessage struct {
	SessionID string `json:"sessionId"`
	IV        string `json:"iv"`
	Data      string `json:"data"`
}
type channelHello struct {
	Features        []string `json:"features,omitempty"`
	Version         int      `json:"v"`
	Kind            string   `json:"kind"`
	DeviceID        string   `json:"deviceId"`
	PublicKey       string   `json:"publicKey"`
	Endpoint        string   `json:"endpoint"`
	Origin          string   `json:"origin"`
	Nonce           string   `json:"nonce"`
	ClientPublicKey string   `json:"clientPublicKey"`
	ServerPublicKey string   `json:"serverPublicKey"`
	Salt            string   `json:"salt"`
	SessionID       string   `json:"sessionId"`
	ExpiresAt       int64    `json:"expiresAt"`
}
type secureChannel struct {
	Fragments bool
	Origin    string
	Expires   time.Time
	Receive   cipher.AEAD
	Send      cipher.AEAD
	Used      map[string]bool
}
type secureRequest struct {
	Version           int    `json:"v"`
	Kind              string `json:"kind"`
	SessionID         string `json:"sessionId"`
	Nonce             string `json:"nonce"`
	Timestamp         int64  `json:"timestamp"`
	RelayConnectionID string `json:"relayConnectionId,omitempty"`
	Deadline          int64  `json:"deadline,omitempty"`
	Origin            string `json:"origin"`
	PairingID         string `json:"pairingId"`
	PublicKey         string `json:"publicKey"`
	Method            string `json:"method"`
	Path              string `json:"path"`
	Authorization     string `json:"authorization,omitempty"`
	Lease             string `json:"lease,omitempty"`
	Body              string `json:"body"`
}
type secureFrame struct {
	Version     int    `json:"v"`
	Kind        string `json:"kind"`
	SessionID   string `json:"sessionId"`
	Nonce       string `json:"nonce"`
	RequestHash string `json:"requestHash"`
	Origin      string `json:"origin"`
	PairingID   string `json:"pairingId"`
	Method      string `json:"method"`
	Path        string `json:"path"`
	Sequence    uint64 `json:"sequence"`
	Previous    string `json:"previous"`
	Status      int    `json:"status"`
	Body        string `json:"body"`
}
type requestIdentity struct {
	PairingID string
	PublicKey string
	ChannelID string
}
type verifiedContextKey struct{}

func publicKey(k *ecdsa.PrivateKey) string {
	return b64.EncodeToString(elliptic.Marshal(k.Curve, k.X, k.Y))
}
func parsePublicKey(raw string) (*ecdsa.PublicKey, error) {
	data, e := b64.DecodeString(raw)
	if e != nil || len(data) != 65 {
		return nil, errors.New("invalid P-256 public key")
	}
	x, y := elliptic.Unmarshal(elliptic.P256(), data)
	if x == nil {
		return nil, errors.New("invalid P-256 public key")
	}
	return &ecdsa.PublicKey{Curve: elliptic.P256(), X: x, Y: y}, nil
}
func signBytes(k *ecdsa.PrivateKey, payload []byte) (signedMessage, error) {
	hash := sha256.Sum256(payload)
	r, s, e := ecdsa.Sign(rand.Reader, k, hash[:])
	if e != nil {
		return signedMessage{}, e
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return signedMessage{Payload: b64.EncodeToString(payload), Signature: b64.EncodeToString(sig)}, nil
}
func verifyBytes(key string, message signedMessage) ([]byte, error) {
	k, e := parsePublicKey(key)
	if e != nil {
		return nil, e
	}
	payload, e := b64.DecodeString(message.Payload)
	if e != nil {
		return nil, e
	}
	sig, e := b64.DecodeString(message.Signature)
	if e != nil || len(sig) != 64 {
		return nil, errors.New("invalid signature")
	}
	hash := sha256.Sum256(payload)
	if !ecdsa.Verify(k, hash[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
		return nil, errors.New("invalid signature")
	}
	return payload, nil
}
func loadIdentity(dir string) (*ecdsa.PrivateKey, error) {
	path := filepath.Join(dir, "identity.key")
	info, e := os.Lstat(path)
	if e == nil {
		if !info.Mode().IsRegular() {
			return nil, errors.New("identity key must be a regular file")
		}
		data, e := ReadLocalFile(path, 4096)
		if e != nil {
			return nil, e
		}
		key, e := x509.ParsePKCS8PrivateKey(data)
		if e != nil {
			return nil, errors.New("identity key is corrupt; restore it or explicitly remove it and re-pair apps")
		}
		k, ok := key.(*ecdsa.PrivateKey)
		if !ok || k.Curve != elliptic.P256() {
			return nil, errors.New("unsupported identity key")
		}
		if e = os.Chmod(path, 0600); e != nil {
			return nil, e
		}
		return k, nil
	}
	if !errors.Is(e, os.ErrNotExist) {
		return nil, e
	}
	k, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		return nil, e
	}
	data, e := x509.MarshalPKCS8PrivateKey(k)
	if e != nil {
		return nil, e
	}
	f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	if _, e = f.Write(data); e != nil {
		return nil, e
	}
	if e = f.Sync(); e != nil {
		return nil, e
	}
	return k, nil
}
func channelCipher(shared, salt []byte, session, direction string) (cipher.AEAD, error) {
	key, e := hkdf.Key(sha256.New, shared, salt, "ai-bridge/v2/"+session+"/"+direction, 32)
	if e != nil {
		return nil, e
	}
	block, e := aes.NewCipher(key)
	if e != nil {
		return nil, e
	}
	return cipher.NewGCM(block)
}
func sealMessage(c cipher.AEAD, session, direction string, data []byte) (encryptedMessage, error) {
	iv := make([]byte, c.NonceSize())
	if _, e := rand.Read(iv); e != nil {
		return encryptedMessage{}, e
	}
	encrypted := c.Seal(nil, iv, data, []byte("ai-bridge/v2/"+session+"/"+direction))
	return encryptedMessage{SessionID: session, IV: b64.EncodeToString(iv), Data: b64.EncodeToString(encrypted)}, nil
}
func openMessage(c cipher.AEAD, message encryptedMessage, direction string) ([]byte, error) {
	iv, e := b64.DecodeString(message.IV)
	if e != nil || len(iv) != c.NonceSize() {
		return nil, errors.New("invalid IV")
	}
	data, e := b64.DecodeString(message.Data)
	if e != nil {
		return nil, e
	}
	return c.Open(nil, iv, data, []byte("ai-bridge/v2/"+message.SessionID+"/"+direction))
}
func decodeSecure(w http.ResponseWriter, r *http.Request, v any) error {
	media, _, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if e != nil || media != "application/json" {
		return fail(415, "json_required", "JSON required.")
	}
	limit := int64(128 << 10)
	if r.URL.Path == "/v2/request" {
		limit = wirelimits.Envelope
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		return fail(400, "invalid_secure_message", "Invalid secure message.")
	}
	if d.Decode(new(any)) != io.EOF {
		return fail(400, "invalid_secure_message", "Exactly one message is required.")
	}
	return nil
}
func (a *App) invitationBundle(token, endpoint string, p Pairing, expires time.Time) string {
	payload, _ := json.Marshal(map[string]any{"v": 2, "connectionReviewVersion": 1, "filePermissionsVersion": 1, "secret": token, "endpoint": endpoint, "deviceId": a.state.DeviceID, "publicKey": publicKey(a.identity), "appId": p.AppID, "appUser": p.AppUser, "origin": p.Origin, "expiresAt": expires.UnixMilli()})
	return "aib2." + b64.EncodeToString(payload)
}
func (a *App) openChannel(w http.ResponseWriter, r *http.Request, endpoint string) error {
	if !exactOrigin(r.Header.Get("Origin")) {
		return fail(403, "origin_required", "A paired application origin is required.")
	}
	var in struct {
		Nonce     string   `json:"nonce"`
		PublicKey string   `json:"publicKey"`
		Features  []string `json:"features,omitempty"`
	}
	if e := decodeSecure(w, r, &in); e != nil {
		return e
	}
	if decoded, e := hex.DecodeString(in.Nonce); e != nil || len(decoded) != 32 {
		return fail(400, "invalid_nonce", "A fresh 256-bit challenge is required.")
	}
	raw, e := b64.DecodeString(in.PublicKey)
	if e != nil {
		return fail(400, "invalid_key", "Invalid channel key.")
	}
	client, e := ecdh.P256().NewPublicKey(raw)
	if e != nil {
		return fail(400, "invalid_key", "Invalid channel key.")
	}
	ephemeral, e := ecdh.P256().GenerateKey(rand.Reader)
	if e != nil {
		return e
	}
	shared, e := ephemeral.ECDH(client)
	if e != nil {
		return e
	}
	salt := make([]byte, 32)
	if _, e = rand.Read(salt); e != nil {
		return e
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	count := 0
	for key, c := range a.channels {
		if !now.Before(c.Expires) {
			delete(a.channels, key)
		} else if c.Origin == r.Header.Get("Origin") {
			count++
		}
	}
	if len(a.channels) >= 256 || count >= 64 {
		return fail(429, "channel_limit", "Too many active secure connections.")
	}
	id := secret()
	expires := now.Add(channelLifetime)
	receive, e := channelCipher(shared, salt, id, "request")
	if e != nil {
		return e
	}
	send, e := channelCipher(shared, salt, id, "response")
	if e != nil {
		return e
	}
	hello := channelHello{Version: 2, Kind: "ai-bridge-channel", DeviceID: a.state.DeviceID, PublicKey: publicKey(a.identity), Endpoint: endpoint, Origin: r.Header.Get("Origin"), Nonce: in.Nonce, ClientPublicKey: in.PublicKey, ServerPublicKey: b64.EncodeToString(ephemeral.PublicKey().Bytes()), Salt: b64.EncodeToString(salt), SessionID: id, ExpiresAt: expires.UnixMilli()}
	for _, feature := range in.Features {
		if feature == "fragments-v1" {
			hello.Features = []string{feature}
		}
	}
	payload, _ := json.Marshal(hello)
	signed, e := signBytes(a.identity, payload)
	if e != nil {
		return e
	}
	a.channels[id] = &secureChannel{Fragments: len(hello.Features) > 0, Origin: hello.Origin, Expires: expires, Receive: receive, Send: send, Used: map[string]bool{}}
	writeJSON(w, 200, signed)
	return nil
}
func (a *App) secureCall(w http.ResponseWriter, r *http.Request, mux http.Handler) error {
	var envelope encryptedMessage
	if e := decodeSecure(w, r, &envelope); e != nil {
		return e
	}
	a.mu.Lock()
	channel := a.channels[envelope.SessionID]
	valid := channel != nil && a.now().Before(channel.Expires) && channel.Origin == r.Header.Get("Origin")
	a.mu.Unlock()
	if !valid {
		return fail(401, "secure_channel_required", "Open a verified secure channel.")
	}
	data, e := openMessage(channel.Receive, envelope, "request")
	if e != nil {
		return fail(400, "invalid_ciphertext", "Secure request verification failed.")
	}
	var signed signedMessage
	if json.Unmarshal(data, &signed) != nil {
		return fail(400, "invalid_secure_message", "Invalid signed request.")
	}
	raw, e := b64.DecodeString(signed.Payload)
	if e != nil {
		return fail(400, "invalid_secure_message", "Invalid signed request.")
	}
	var in secureRequest
	if json.Unmarshal(raw, &in) != nil {
		return fail(400, "invalid_secure_message", "Invalid signed request.")
	}
	if _, e = verifyBytes(in.PublicKey, signed); e != nil {
		return fail(401, "invalid_signature", "Request signature verification failed.")
	}
	if in.Version != 2 || in.Kind != "ai-bridge-request" || in.SessionID != envelope.SessionID || in.Origin != channel.Origin {
		return fail(400, "invalid_binding", "Invalid secure request binding.")
	}
	nonce, e := hex.DecodeString(in.Nonce)
	if e != nil || len(nonce) != 32 {
		return fail(400, "invalid_nonce", "Invalid request nonce.")
	}
	// Logical routes are narrowly scoped: a paired app can never tunnel control-panel calls.
	if !strings.HasPrefix(in.Path, "/v2/") || strings.ContainsAny(in.Path, "?#%\\") || strings.Contains(in.Path, "..") || in.Path == "/v2/channel" || in.Path == "/v2/request" {
		return fail(400, "invalid_path", "Invalid resource path.")
	}
	if in.Method != "GET" && in.Method != "POST" && in.Method != "PUT" && in.Method != "DELETE" {
		return fail(400, "invalid_method", "Invalid resource method.")
	}
	a.mu.Lock()
	now := a.now()
	age := now.Sub(time.UnixMilli(in.Timestamp))
	if age > time.Minute || age < -time.Minute {
		a.mu.Unlock()
		return fail(401, "stale_request", "Request timestamp is outside the allowed window.")
	}
	// Relay messages are signed for one connection and deadline. Direct v2 clients
	// retain their existing timestamp/nonce binding.
	var relayBinding *RelayBinding
	if p := a.state.Pairings[in.PairingID]; p != nil {
		relayBinding = p.Relay
	} else if in.Path == "/v2/pairings/claim" {
		if invitation := a.state.Invitations[digest(in.Authorization)]; invitation != nil {
			relayBinding = invitation.Pairing.Relay
		}
	}
	if relayBinding != nil && (in.RelayConnectionID != relayBinding.ConnectionID ||
		in.Deadline <= now.UnixMilli() || in.Deadline > now.Add(time.Minute).UnixMilli()) {
		a.mu.Unlock()
		return fail(401, "relay_binding_mismatch", "Relay connection or deadline mismatch.")
	}
	if channel.Used[in.Nonce] || len(channel.Used) >= 2048 {
		a.mu.Unlock()
		return fail(409, "request_replay", "Request nonce was consumed or channel capacity was reached.")
	}
	channel.Used[in.Nonce] = true
	a.mu.Unlock()
	inner, e := http.NewRequestWithContext(r.Context(), in.Method, "http://"+r.Host+in.Path, strings.NewReader(in.Body))
	if e != nil {
		return e
	}
	inner.RemoteAddr = r.RemoteAddr
	inner.Header.Set("Origin", in.Origin)
	inner.Header.Set("Content-Type", "application/json")
	inner.Header.Set("X-Bridge-Lease", in.Lease)
	if in.Path == "/v2/pairings/claim" {
		inner.Header.Set("Authorization", "Bearer "+in.Authorization)
	} else if in.Authorization != "" {
		return fail(400, "invalid_authorization", "Invitation secrets are accepted only during pairing.")
	}
	inner = inner.WithContext(context.WithValue(inner.Context(), verifiedContextKey{}, requestIdentity{PairingID: in.PairingID, PublicKey: in.PublicKey, ChannelID: in.SessionID}))
	ctx, cancel := context.WithCancel(inner.Context())
	defer cancel()
	inner = inner.WithContext(ctx)
	sw := &secureWriter{target: w, header: http.Header{}, status: 200, app: a, channel: channel, request: in, requestHash: digest(string(raw)), stream: in.Method == "GET" && strings.HasSuffix(in.Path, "/events"), cancel: cancel}
	a.mu.Lock()
	maintenance := a.updating
	a.mu.Unlock()
	if maintenance {
		writeError(sw, fail(503, "bridge_updating", "WebDeviceCable is updating. Reconnect in a moment."))
		return sw.finish()
	}
	if code := wirelimits.Check(in.Method, in.Path, in.Body); code != "" {
		writeError(sw, fail(413, code, "The authenticated request exceeds its size budget."))
		return sw.finish()
	}
	mux.ServeHTTP(sw, inner)
	if err := sw.finish(); err != nil {
		return err
	}
	complete := func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		for _, delivered := range sw.delivered {
			delivered()
		}
	}
	// Relay exchanges also need their existing transport end marker accepted.
	// Writing a signed body alone is not yet a complete relay HTTP response.
	if relay, ok := w.(*relayWriter); ok {
		relay.delivered = append(relay.delivered, complete)
	} else {
		complete()
	}

	return nil
}

// secureWriter preserves native HTTP handlers while authenticating every emitted
// snapshot before a consumer sees it. Stream chunks are signed independently;
// a hash chain and authenticated end frame detect removal/reordering/truncation.
type secureWriter struct {
	delivered   []func()
	target      http.ResponseWriter
	header      http.Header
	status      int
	buffer      bytes.Buffer
	app         *App
	channel     *secureChannel
	request     secureRequest
	requestHash string
	sequence    uint64
	previous    string
	stream      bool
	started     bool
	ended       bool
	fragmenting bool
	err         error
	cancel      context.CancelFunc
}

func (s *secureWriter) Header() http.Header    { return s.header }
func (s *secureWriter) WriteHeader(status int) { s.status = status }
func (s *secureWriter) Write(data []byte) (int, error) {
	if s.err != nil {
		return 0, s.err
	}
	if s.buffer.Len()+len(data) > 52<<20 {
		s.err = errors.New("secure response exceeds limit")
		s.cancel()
		return 0, s.err
	}
	return s.buffer.Write(data)
}
func (s *secureWriter) emit(kind, body string) error {
	if kind != "fragment" && len(body) > 16<<10 && s.channel.Fragments {
		s.fragmenting = true
		for len(body) > 0 {
			// Base64 keeps individual fragments independent of UTF-8 boundaries.
			if err := s.emit("fragment", b64.EncodeToString([]byte(body[:min(len(body), 16<<10)]))); err != nil {
				return err
			}
			body = body[min(len(body), 16<<10):]
		}
	}

	frame := secureFrame{Version: 2, Kind: kind, SessionID: s.request.SessionID, Nonce: s.request.Nonce, RequestHash: s.requestHash, Origin: s.request.Origin, PairingID: s.request.PairingID, Method: s.request.Method, Path: s.request.Path, Sequence: s.sequence, Previous: s.previous, Status: s.status, Body: body}
	payload, e := json.Marshal(frame)
	if e != nil {
		return e
	}
	signed, e := signBytes(s.app.identity, payload)
	if e != nil {
		return e
	}
	encoded, e := json.Marshal(signed)
	if e != nil {
		return e
	}
	encrypted, e := sealMessage(s.channel.Send, s.request.SessionID, "response", encoded)
	if e != nil {
		return e
	}
	wire, e := json.Marshal(encrypted)
	if e != nil {
		return e
	}
	if s.fragmenting || s.stream && strings.HasPrefix(s.header.Get("Content-Type"), "text/event-stream") {
		if !s.started {
			s.target.Header().Set("Content-Type", "text/event-stream")
			s.target.Header().Set("X-Accel-Buffering", "no")
			s.started = true
		}
		if _, e = fmt.Fprintf(s.target, "event: bridge\ndata: %s\n\n", wire); e != nil {
			return e
		}
		if flusher, ok := s.target.(http.Flusher); ok {
			flusher.Flush()
		}
	} else {
		s.target.Header().Set("Content-Type", "application/json")
		_, e = s.target.Write(wire)
		if e != nil {
			return e
		}
	}
	s.sequence++
	s.previous = digest(string(payload))
	if kind == "end" {
		s.ended = true
	}
	return nil
}
func (s *secureWriter) Flush() {
	if s.err != nil || s.ended {
		return
	}
	text := s.buffer.String()
	if !strings.HasSuffix(text, "\n\n") {
		return
	}
	s.buffer.Reset()
	for _, event := range strings.Split(strings.TrimSuffix(text, "\n\n"), "\n\n") {
		body := ""
		for _, line := range strings.Split(event, "\n") {
			if strings.HasPrefix(line, "data: ") {
				body = strings.TrimPrefix(line, "data: ")
			}
		}
		if body == "" {
			continue
		}
		var j Job
		if e := json.Unmarshal([]byte(body), &j); e != nil {
			s.err = e
			s.cancel()
			return
		}
		kind := "event"
		if terminal(j.Status) {
			kind = "end"
		}
		if e := s.emit(kind, body); e != nil {
			s.err = e
			s.cancel()
			return
		}
	}
}
func (s *secureWriter) finish() error {
	if s.err != nil {
		return s.err
	}
	if s.ended {
		return nil
	}
	if s.started {
		return s.emit("end", "")
	}
	return s.emit("response", s.buffer.String())
}
