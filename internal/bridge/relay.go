package bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/thomasmikava/WebDeviceCable/internal/wirelimits"
)

// RelayBinding is private local connector state. publicPairing always removes it.
type RelayBinding struct {
	URL          string `json:"url"`
	ConnectionID string `json:"connectionId"`
	Token        string `json:"token"`
}
type relayInput struct {
	URL  string `json:"url"`
	Code string `json:"code"`
}
type relayInfo struct {
	AppID        string `json:"appId"`
	Name         string `json:"name"`
	Origin       string `json:"origin"`
	AppUser      string `json:"appUser"`
	ConnectionID string `json:"connectionId"`
}
type relayPacket struct {
	ID       string `json:"id"`
	Path     string `json:"path"`
	Body     string `json:"body"`
	Deadline int64  `json:"deadline"`
}
type relayResult struct {
	ID       string `json:"id"`
	Sequence int    `json:"sequence"`
	Status   int    `json:"status"`
	Body     string `json:"body"`
	End      bool   `json:"end"`
}

func relayHTTPClient() *http.Client {
	return &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("relay redirects denied") }}
}
func checkRelayURL(raw string) error {
	u, e := url.Parse(raw)
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
		return errors.New("use the exact relay HTTPS origin")
	}
	if u.Scheme == "https" {
		return nil
	}
	ip := net.ParseIP(u.Hostname())
	if u.Scheme == "http" && ip != nil && ip.IsLoopback() {
		return nil
	}
	return errors.New("HTTPS required; HTTP is allowed only for a loopback IP")
}
func relayJSON(ctx context.Context, client *http.Client, endpoint, token string, in, out any) (int, error) {
	body, e := json.Marshal(in)
	if e != nil {
		return 0, e
	}
	req, e := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(body))
	if e != nil {
		return 0, e
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, e := client.Do(req)
	if e != nil {
		return 0, errors.New("relay connection unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return resp.StatusCode, errors.New("relay rejected this operation")
	}
	if out != nil {
		limit := 2 << 20
		if strings.HasSuffix(endpoint, "/poll") {
			limit = wirelimits.Poll
		}
		raw, err := io.ReadAll(io.LimitReader(resp.Body, int64(limit+1)))
		if err != nil {
			return resp.StatusCode, err
		}
		if len(raw) > limit {
			return resp.StatusCode, errors.New("relay response exceeds its size limit")
		}
		e = json.Unmarshal(raw, out)
	} else {
		_, e = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	}
	return resp.StatusCode, e
}
func inspectRelay(ctx context.Context, in relayInput) (relayInfo, error) {
	var info relayInfo
	if checkRelayURL(in.URL) != nil || !validStorageID(in.Code) {
		return info, fail(400, "invalid_relay", "Use the relay origin and its current single-use pairing code.")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, e := relayJSON(ctx, relayHTTPClient(), in.URL+"/connector/v1/inspect", "", map[string]string{"code": in.Code}, &info)
	if e != nil {
		return info, fail(502, "relay_unavailable", "The relay or pairing code is unavailable.")
	}
	if e = validatePairing(Pairing{AppID: info.AppID, Name: info.Name, Origin: info.Origin, AppUser: info.AppUser, Mode: "restricted"}); e != nil || !validStorageID(info.ConnectionID) {
		return info, fail(400, "invalid_relay", "The relay returned an invalid project identity.")
	}
	return info, nil
}
func (a *App) relayRoutes(mux *http.ServeMux, control func(func(http.ResponseWriter, *http.Request) error) http.HandlerFunc, origin string) {
	mux.HandleFunc("POST /api/relay/inspect", control(func(w http.ResponseWriter, r *http.Request) error {
		var in relayInput
		if e := decode(w, r, &in); e != nil {
			return e
		}
		info, e := inspectRelay(r.Context(), in)
		if e != nil {
			return e
		}
		writeJSON(w, 200, info)
		return nil
	}))
	mux.HandleFunc("POST /api/relay/decline", control(func(w http.ResponseWriter, r *http.Request) error {
		var in relayInput
		if err := decode(w, r, &in); err != nil {
			return err
		}
		if _, err := inspectRelay(r.Context(), in); err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if _, err := relayJSON(ctx, relayHTTPClient(), in.URL+"/connector/v1/decline", "", map[string]string{"code": in.Code}, nil); err != nil {
			return fail(502, "relay_unavailable", "Could not decline this invitation. Try again.")
		}
		writeJSON(w, 200, map[string]bool{"declined": true})
		return nil
	}))
	mux.HandleFunc("POST /api/relay/connect", control(func(w http.ResponseWriter, r *http.Request) error {
		var in struct {
			URL      string    `json:"url"`
			Code     string    `json:"code"`
			Expected relayInfo `json:"expected"`
		}
		if e := decode(w, r, &in); e != nil {
			return e
		}
		info, e := inspectRelay(r.Context(), relayInput{in.URL, in.Code})
		if e != nil {
			return e
		}
		if info != in.Expected {
			return fail(409, "relay_identity_changed", "Review the relay project and account again.")
		}
		result, e := a.createInvitation(invitationInput{AppID: info.AppID, Name: info.Name, Origin: info.Origin, AppUser: info.AppUser}, origin)
		if e != nil {
			return e
		}
		invitation := result["invitation"].(string)
		raw, _ := b64.DecodeString(strings.TrimPrefix(invitation, "aib2."))
		var invite struct{ Secret string }
		_ = json.Unmarshal(raw, &invite)
		binding := &RelayBinding{URL: in.URL, ConnectionID: info.ConnectionID, Token: secret()}
		if e = a.update(func(s *State) error {
			p := &s.Invitations[digest(invite.Secret)].Pairing
			p.Transport = "relay"
			p.Relay = binding
			return nil
		}); e != nil {
			return e
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		var claim struct{ ConnectionID string }
		if _, e = relayJSON(ctx, relayHTTPClient(), in.URL+"/connector/v1/claim", "", map[string]string{"code": in.Code, "invitation": invitation, "token": binding.Token}, &claim); e != nil || claim.ConnectionID != info.ConnectionID {
			return fail(502, "relay_enrollment_unknown", "Enrollment was not confirmed. Inspect the relay before creating another code.")
		}
		// control holds the app lock; start without trying to take it again.
		a.startRelayLocked(mux, origin, info.Origin, result["pairing"].(Pairing).ID, binding)
		writeJSON(w, 201, map[string]string{"connectionId": binding.ConnectionID})
		return nil
	}))
}
func (a *App) resumeRelays(mux http.Handler, origin string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, p := range a.state.Pairings {
		if p.Relay != nil && !p.Revoked {
			a.startRelayLocked(mux, origin, p.Origin, p.ID, p.Relay)
		}
	}
}
func (a *App) startRelayLocked(mux http.Handler, endpoint, appOrigin, pairID string, binding *RelayBinding) {
	if a.relayCancels == nil {
		a.relayCancels = map[string]context.CancelFunc{}
	}
	if a.closed || a.relayCancels[pairID] != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.relayCancels[pairID] = cancel
	copy := *binding
	go a.relayLoop(ctx, mux, endpoint, appOrigin, pairID, copy)
}
func (a *App) relayLoop(ctx context.Context, mux http.Handler, endpoint, appOrigin, pairID string, binding RelayBinding) {
	client := relayHTTPClient()
	slots := make(chan struct{}, 16)
	for ctx.Err() == nil {
		a.mu.Lock()
		p := a.state.Pairings[pairID]
		stop := a.closed || p != nil && p.Revoked
		a.mu.Unlock()
		if stop {
			return
		}
		var response struct {
			Packets []relayPacket `json:"packets"`
		}
		status, e := relayJSON(ctx, client, binding.URL+"/connector/v1/"+binding.ConnectionID+"/poll", binding.Token, struct{}{}, &response)
		if e != nil {
			if status == 401 {
				a.mu.Lock()
				if !a.closed {
					_ = a.update(func(s *State) error {
						if p := s.Pairings[pairID]; p != nil {
							p.Paused = true
							for _, j := range s.Jobs {
								if j.PairingID == pairID && !terminal(j.Status) {
									finish(j, "cancelled", "relay_revoked", "The backend revoked this relay connection.", a.now())
								}
							}
						}
						return nil
					})
				}
				a.mu.Unlock()
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
			continue
		}
		packetBytes := 0
		for _, packet := range response.Packets {
			packetBytes += len(packet.Body)
		}
		if len(response.Packets) > 8 || packetBytes > wirelimits.Envelope {
			continue
		}
		for _, packet := range response.Packets {
			packetLimit := wirelimits.Envelope
			if packet.Path == "/v2/channel" {
				packetLimit = 128 << 10
			}
			if (packet.Path != "/v2/channel" && packet.Path != "/v2/request") || !validStorageID(packet.ID) || packet.Deadline <= time.Now().UnixMilli() || packet.Deadline > time.Now().Add(time.Minute).UnixMilli() || len(packet.Body) > packetLimit {
				continue
			}
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				return
			}
			go func(packet relayPacket) {
				defer func() { <-slots }()
				work, cancel := context.WithDeadline(ctx, time.UnixMilli(packet.Deadline))
				defer cancel()
				req, e := http.NewRequestWithContext(work, "POST", endpoint+packet.Path, strings.NewReader(packet.Body))
				if e != nil {
					return
				}
				req.RemoteAddr = "127.0.0.1:1"
				req.Header.Set("Origin", appOrigin)
				req.Header.Set("Content-Type", "application/json")
				writer := &relayWriter{ctx: work, client: client, binding: binding, id: packet.ID, header: http.Header{}, status: 200}
				mux.ServeHTTP(writer, req)
				writer.finish()
			}(packet)
		}
	}
}

type relayWriter struct {
	ctx              context.Context
	client           *http.Client
	binding          RelayBinding
	id               string
	header           http.Header
	status, sequence int
	err              error
}

func (w *relayWriter) Header() http.Header    { return w.header }
func (w *relayWriter) WriteHeader(status int) { w.status = status }
func (w *relayWriter) Flush()                 {}
func (w *relayWriter) Write(b []byte) (int, error) {
	total := len(b)
	for len(b) > 0 {
		n := min(len(b), 48<<10)
		if e := w.send(string(b[:n]), false); e != nil {
			return 0, e
		}
		b = b[n:]
	}
	return total, nil
}
func (w *relayWriter) send(body string, end bool) error {
	if w.err != nil {
		return w.err
	}
	_, w.err = relayJSON(w.ctx, w.client, w.binding.URL+"/connector/v1/"+w.binding.ConnectionID+"/result", w.binding.Token, relayResult{ID: w.id, Sequence: w.sequence, Status: w.status, Body: body, End: end}, nil)
	w.sequence++
	return w.err
}
func (w *relayWriter) finish() { _ = w.send("", true) }
