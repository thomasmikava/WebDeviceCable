package relay

import (
	"bufio"
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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"
)

var b64 = base64.RawURLEncoding

type invitation struct {
	ConnectionReviewVersion int    `json:"connectionReviewVersion,omitempty"`
	V                       int    `json:"v"`
	Secret                  string `json:"secret"`
	Endpoint                string `json:"endpoint"`
	DeviceID                string `json:"deviceId"`
	PublicKey               string `json:"publicKey"`
	AppID                   string `json:"appId"`
	AppUser                 string `json:"appUser"`
	Origin                  string `json:"origin"`
	ExpiresAt               int64  `json:"expiresAt"`
}
type signed struct {
	Payload   string `json:"payload"`
	Signature string `json:"signature"`
}
type envelope struct {
	SessionID string `json:"sessionId"`
	IV        string `json:"iv"`
	Data      string `json:"data"`
}
type channel struct {
	id            string
	send, receive cipher.AEAD
	expires       int64
	requests      int
}
type streamedBody struct {
	io.ReadCloser
	source io.Closer
}

func (b *streamedBody) Close() error { err := b.ReadCloser.Close(); _ = b.source.Close(); return err }

type v2Client struct {
	mu        sync.Mutex
	c         *connection
	key       *ecdsa.PrivateKey
	channel   *channel
	transport func(context.Context, string, []byte) (*http.Response, error)
	save      func() error
}

func parseInvitation(raw string) (invitation, error) {
	var v invitation
	if !strings.HasPrefix(raw, "aib2.") || len(raw) > 8192 {
		return v, errors.New("invalid invitation")
	}
	b, e := b64.DecodeString(raw[5:])
	if e != nil {
		return v, e
	}
	e = json.Unmarshal(b, &v)
	if e != nil || v.V != 2 || v.ExpiresAt < time.Now().UnixMilli() || len(v.Secret) != 64 || len(v.DeviceID) != 64 {
		return v, errors.New("invalid invitation")
	}
	if _, e = parsePublic(v.PublicKey); e != nil {
		return v, e
	}
	return v, nil
}
func newPrivate() (string, error) {
	k, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		return "", e
	}
	b, e := x509.MarshalPKCS8PrivateKey(k)
	return b64.EncodeToString(b), e
}
func makeV2Client(c *connection, transport func(context.Context, string, []byte) (*http.Response, error), save func() error) (*v2Client, error) {
	b, e := b64.DecodeString(c.PrivateKey)
	if e != nil {
		return nil, e
	}
	key, e := x509.ParsePKCS8PrivateKey(b)
	if e != nil {
		return nil, e
	}
	k, ok := key.(*ecdsa.PrivateKey)
	if !ok {
		return nil, errors.New("invalid key")
	}
	return &v2Client{c: c, key: k, transport: transport, save: save}, nil
}
func parsePublic(raw string) (*ecdsa.PublicKey, error) {
	b, e := b64.DecodeString(raw)
	if e != nil {
		return nil, e
	}
	x, y := elliptic.Unmarshal(elliptic.P256(), b)
	if x == nil {
		return nil, errors.New("invalid public key")
	}
	return &ecdsa.PublicKey{Curve: elliptic.P256(), X: x, Y: y}, nil
}
func verify(pin string, s signed) ([]byte, error) {
	k, e := parsePublic(pin)
	if e != nil {
		return nil, e
	}
	p, e := b64.DecodeString(s.Payload)
	if e != nil {
		return nil, e
	}
	sig, e := b64.DecodeString(s.Signature)
	if e != nil || len(sig) != 64 {
		return nil, errors.New("invalid signature")
	}
	h := sha256.Sum256(p)
	if !ecdsa.Verify(k, h[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
		return nil, errors.New("invalid signature")
	}
	return p, nil
}
func sign(k *ecdsa.PrivateKey, p []byte) (signed, error) {
	h := sha256.Sum256(p)
	r, s, e := ecdsa.Sign(rand.Reader, k, h[:])
	if e != nil {
		return signed{}, e
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return signed{b64.EncodeToString(p), b64.EncodeToString(sig)}, nil
}
func derive(shared, salt []byte, id, direction string) (cipher.AEAD, error) {
	k, e := hkdf.Key(sha256.New, shared, salt, "ai-bridge/v2/"+id+"/"+direction, 32)
	if e != nil {
		return nil, e
	}
	b, e := aes.NewCipher(k)
	if e != nil {
		return nil, e
	}
	return cipher.NewGCM(b)
}
func seal(c cipher.AEAD, id, direction string, p []byte) (envelope, error) {
	iv := make([]byte, 12)
	if _, e := rand.Read(iv); e != nil {
		return envelope{}, e
	}
	return envelope{id, b64.EncodeToString(iv), b64.EncodeToString(c.Seal(nil, iv, p, []byte("ai-bridge/v2/"+id+"/"+direction)))}, nil
}
func unseal(c cipher.AEAD, id, direction string, v envelope) ([]byte, error) {
	if v.SessionID != id {
		return nil, errors.New("channel mismatch")
	}
	iv, e := b64.DecodeString(v.IV)
	if e != nil || len(iv) != 12 {
		return nil, errors.New("invalid nonce")
	}
	data, e := b64.DecodeString(v.Data)
	if e != nil {
		return nil, e
	}
	return c.Open(nil, iv, data, []byte("ai-bridge/v2/"+id+"/"+direction))
}
func (c *v2Client) open(ctx context.Context) (*channel, error) {
	if c.channel != nil && c.channel.requests < 1000 && c.channel.expires > time.Now().Add(time.Minute).UnixMilli() {
		c.channel.requests++
		return c.channel, nil
	}
	key, e := ecdh.P256().GenerateKey(rand.Reader)
	if e != nil {
		return nil, e
	}
	nonce := ID()
	pk := b64.EncodeToString(key.PublicKey().Bytes())
	body, _ := json.Marshal(map[string]any{"nonce": nonce, "publicKey": pk, "features": []string{"fragments-v1"}})
	res, e := c.transport(ctx, "/v2/channel", body)
	if e != nil {
		return nil, e
	}
	defer res.Body.Close()
	var s signed
	if e = json.NewDecoder(io.LimitReader(res.Body, 128<<10)).Decode(&s); e != nil {
		return nil, e
	}
	raw, e := verify(c.c.Identity.PublicKey, s)
	if e != nil {
		return nil, e
	}
	var hello struct {
		V                                                                                                     int
		Kind, DeviceID, PublicKey, Endpoint, Origin, Nonce, ClientPublicKey, ServerPublicKey, Salt, SessionID string
		ExpiresAt                                                                                             int64
	}
	if json.Unmarshal(raw, &hello) != nil || hello.V != 2 || hello.Kind != "ai-bridge-channel" || hello.DeviceID != c.c.Identity.DeviceID || hello.PublicKey != c.c.Identity.PublicKey || hello.Endpoint != c.c.Identity.Endpoint || hello.Origin != c.c.Identity.Origin || hello.Nonce != nonce || hello.ClientPublicKey != pk || hello.ExpiresAt <= time.Now().UnixMilli() || hello.ExpiresAt > time.Now().Add(16*time.Minute).UnixMilli() {
		return nil, errors.New("pinned handshake mismatch")
	}
	peerBytes, e := b64.DecodeString(hello.ServerPublicKey)
	if e != nil {
		return nil, e
	}
	peer, e := ecdh.P256().NewPublicKey(peerBytes)
	if e != nil {
		return nil, e
	}
	shared, e := key.ECDH(peer)
	if e != nil {
		return nil, e
	}
	salt, e := b64.DecodeString(hello.Salt)
	if e != nil || len(salt) != 32 {
		return nil, errors.New("invalid salt")
	}
	ch := &channel{id: hello.SessionID, expires: hello.ExpiresAt, requests: 1}
	ch.send, e = derive(shared, salt, ch.id, "request")
	if e != nil {
		return nil, e
	}
	ch.receive, e = derive(shared, salt, ch.id, "response")
	if e != nil {
		return nil, e
	}
	c.channel = ch
	return ch, nil
}
func (c *v2Client) call(ctx context.Context, method, path, body, lease, authorization string) (*http.Response, error) {
	c.mu.Lock()
	ch, e := c.open(ctx)
	if e != nil {
		c.mu.Unlock()
		return nil, e
	}
	if c.c.PairingID == "" {
		claim, _ := json.Marshal(map[string]string{"appId": c.c.Identity.AppID, "appUser": c.c.Identity.AppUser})
		resp, err := c.exchange(ctx, ch, "", "POST", "/v2/pairings/claim", string(claim), "", c.c.Identity.Secret)
		if err != nil {
			c.mu.Unlock()
			return nil, err
		}
		var result struct {
			Pairing struct{ ID, AppID, AppUser, Origin, DeviceID string }
		}
		err = json.NewDecoder(io.LimitReader(resp.Body, 128<<10)).Decode(&result)
		resp.Body.Close()
		if err != nil || resp.StatusCode != 201 || len(result.Pairing.ID) != 64 || result.Pairing.AppID != c.c.Identity.AppID || result.Pairing.AppUser != c.c.Identity.AppUser || result.Pairing.DeviceID != c.c.Identity.DeviceID || result.Pairing.Origin != c.c.Identity.Origin {
			c.mu.Unlock()
			return nil, errors.New("secure pairing failed")
		}
		c.c.PairingID = result.Pairing.ID
		c.c.Invitation = ""
		c.c.Identity.Secret = ""
		if e = c.save(); e != nil {
			c.mu.Unlock()
			return nil, e
		}
	}
	pairing := c.c.PairingID
	c.mu.Unlock()
	response, err := c.exchange(ctx, ch, pairing, method, path, body, lease, authorization)
	if err != nil {
		c.mu.Lock()
		if c.channel == ch {
			c.channel = nil
		}
		c.mu.Unlock()
	}
	return response, err
}
func (c *v2Client) exchange(ctx context.Context, ch *channel, pairing, method, path, body, lease, authorization string) (*http.Response, error) {
	nonce := ID()
	binding := map[string]any{"v": 2, "kind": "ai-bridge-request", "sessionId": ch.id, "nonce": nonce, "timestamp": time.Now().UnixMilli(), "origin": c.c.Identity.Origin, "pairingId": pairing, "publicKey": b64.EncodeToString(elliptic.Marshal(c.key.Curve, c.key.X, c.key.Y)), "method": method, "path": path, "body": body}
	binding["relayConnectionId"] = c.c.ID
	binding["deadline"] = time.Now().Add(40 * time.Second).UnixMilli()
	if lease != "" {
		binding["lease"] = lease
	}
	if authorization != "" {
		binding["authorization"] = authorization
	}
	// This is signed JSON data, not HTML. Escaping '<', '>' and '&' here can
	// expand an admitted catalog beyond the encrypted-envelope byte budget.
	var payload bytes.Buffer
	encoder := json.NewEncoder(&payload)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(binding); err != nil {
		return nil, err
	}
	raw := bytes.TrimSuffix(payload.Bytes(), []byte("\n"))
	signature, e := sign(c.key, raw)
	if e != nil {
		return nil, e
	}
	encoded, _ := json.Marshal(signature)
	env, e := seal(ch.send, ch.id, "request", encoded)
	if e != nil {
		return nil, e
	}
	wire, _ := json.Marshal(env)
	res, e := c.transport(ctx, "/v2/request", wire)
	if e != nil {
		return nil, e
	}
	type frame struct {
		V                                                                                    int
		Kind, SessionID, Nonce, RequestHash, Origin, PairingID, Method, Path, Previous, Body string
		Sequence                                                                             uint64
		Status                                                                               int
	}
	sequence := uint64(0)
	previous := ""
	fragments := bytes.Buffer{}
	decode := func(line []byte) (frame, error) {
		var env envelope
		var out frame
		var s signed
		if json.Unmarshal(line, &env) != nil {
			return out, errors.New("malformed encrypted frame")
		}
		opened, e := unseal(ch.receive, ch.id, "response", env)
		if e != nil {
			return out, e
		}
		if json.Unmarshal(opened, &s) != nil {
			return out, errors.New("malformed signed frame")
		}
		p, e := verify(c.c.Identity.PublicKey, s)
		if e != nil {
			return out, e
		}
		if json.Unmarshal(p, &out) != nil || out.V != 2 || out.SessionID != ch.id || out.Nonce != nonce || out.RequestHash != hash(string(raw)) || out.Origin != c.c.Identity.Origin || out.PairingID != pairing || out.Method != method || out.Path != path || out.Sequence != sequence || out.Previous != previous {
			return out, errors.New("response binding mismatch")
		}
		sequence++
		previous = hash(string(p))
		if out.Kind == "fragment" {
			b, e := b64.DecodeString(out.Body)
			if e != nil || len(b) > 16384 || fragments.Len()+len(b) > 52<<20 {
				return out, errors.New("invalid fragments")
			}
			fragments.Write(b)
		} else if fragments.Len() > 0 {
			fragments.WriteString(out.Body)
			out.Body = fragments.String()
			fragments.Reset()
		}
		return out, nil
	}
	reader := bufio.NewReaderSize(res.Body, 64<<10)
	first, e := reader.Peek(1)
	if e != nil {
		res.Body.Close()
		return nil, e
	}
	if first[0] == '{' {
		b, e := io.ReadAll(io.LimitReader(reader, 512<<10))
		res.Body.Close()
		if e != nil {
			return nil, e
		}
		f, e := decode(b)
		if e != nil || f.Kind != "response" {
			return nil, errors.New("unsigned or invalid response")
		}
		return &http.Response{StatusCode: f.Status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(f.Body))}, nil
	}
	scan := bufio.NewScanner(reader)
	scan.Buffer(make([]byte, 4096), 512<<10)
	next := func() (*frame, error) {
		for scan.Scan() {
			line := scan.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			f, e := decode([]byte(line[6:]))
			if e != nil {
				return nil, e
			}
			if f.Kind == "fragment" {
				continue
			}
			return &f, nil
		}
		if scan.Err() != nil {
			return nil, scan.Err()
		}
		return nil, errors.New("stream truncated before authenticated end")
	}
	firstFrame, e := next()
	if e != nil {
		res.Body.Close()
		return nil, e
	}
	if firstFrame.Kind == "response" {
		res.Body.Close()
		return &http.Response{StatusCode: firstFrame.Status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(firstFrame.Body))}, nil
	}
	pr, pw := io.Pipe()
	go func() {
		defer res.Body.Close()
		f := firstFrame
		for {
			if f.Kind != "event" && f.Kind != "end" {
				pw.CloseWithError(errors.New("invalid stream kind"))
				return
			}
			if f.Body != "" {
				var job struct {
					Revision uint64
					Status   string
				}
				if json.Unmarshal([]byte(f.Body), &job) != nil {
					pw.CloseWithError(errors.New("invalid job"))
					return
				}
				terminal := strings.Contains("|completed|cancelled|timed_out|blocked|interrupted|failed|", "|"+job.Status+"|")
				if terminal != (f.Kind == "end") {
					pw.CloseWithError(errors.New("unauthenticated terminal job"))
					return
				}
				if _, e = fmt.Fprintf(pw, "id: %d\nevent: job\ndata: %s\n\n", job.Revision, f.Body); e != nil {
					return
				}
			}
			if f.Kind == "end" {
				pw.Close()
				return
			}
			f, e = next()
			if e != nil {
				pw.CloseWithError(e)
				return
			}
		}
	}()
	return &http.Response{StatusCode: firstFrame.Status, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: &streamedBody{ReadCloser: pr, source: res.Body}}, nil
}
