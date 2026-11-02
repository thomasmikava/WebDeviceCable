// Package relay carries the existing signed, encrypted WDC protocol over outbound
// desktop connections. It never exposes the desktop's control API.
package relay

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gofrs/flock"
	_ "modernc.org/sqlite"
)

const Grace = 45 * time.Second

type Config struct{ Directory, AdminKey string }
type ticket struct {
	ProjectID, AppUser, CodeHash, ConnectionID string
	Expires                                    time.Time
	Declined                                   bool
}
type connection struct {
	ID         string
	ProjectID  string
	AppUser    string
	TokenHash  string
	Invitation string
	Identity   invitation
	PrivateKey string
	PairingID  string
	Revoked    bool
}
type Packet struct {
	ID       string `json:"id"`
	Path     string `json:"path"`
	Body     string `json:"body"`
	Deadline int64  `json:"deadline"`
}
type Result struct {
	ID       string `json:"id"`
	Sequence int    `json:"sequence"`
	Status   int    `json:"status"`
	Body     string `json:"body"`
	End      bool   `json:"end"`
}
type operation struct {
	packet     Packet
	connection string
	claimed    bool
	results    chan Result
	sequence   int
	size       int
	done       chan struct{}
	cancel     context.CancelFunc
}
type protocolClient interface {
	call(context.Context, string, string, string, string, string) (*http.Response, error)
}
type Server struct {
	config      Config
	db          *sql.DB
	lock        *flock.Flock
	mu          sync.Mutex
	connections map[string]*connection
	projects    map[string]*projectRecord
	credentials map[string]string
	clients     map[string]protocolClient
	operations  map[string]*operation
	lastSeen    map[string]time.Time
	closed      chan struct{}
	closeOnce   sync.Once
}

func ID() string {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}
func hash(v string) string { s := sha256.Sum256([]byte(v)); return hex.EncodeToString(s[:]) }
func same(a, b string) bool {
	return len(a) == len(b) && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
func Open(config Config) (*Server, error) {
	if e := validateConfig(config); e != nil {
		return nil, e
	}
	if e := os.MkdirAll(config.Directory, 0700); e != nil {
		return nil, e
	}
	if e := os.Chmod(config.Directory, 0700); e != nil {
		return nil, e
	}
	lock := flock.New(filepath.Join(config.Directory, "relay.lock"))
	ok, e := lock.TryLock()
	if e != nil || !ok {
		return nil, errors.New("relay directory already in use")
	}
	db, e := sql.Open("sqlite", filepath.Join(config.Directory, "relay.sqlite"))
	if e != nil {
		lock.Close()
		return nil, e
	}
	db.SetMaxOpenConns(1)
	_, e = db.Exec("PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000; PRAGMA max_page_count=131072; PRAGMA journal_size_limit=1048576; CREATE TABLE IF NOT EXISTS records(kind TEXT NOT NULL,id TEXT NOT NULL,data TEXT NOT NULL,updated INTEGER NOT NULL,PRIMARY KEY(kind,id));")
	if e != nil {
		db.Close()
		lock.Close()
		return nil, e
	}
	_ = os.Chmod(filepath.Join(config.Directory, "relay.sqlite"), 0600)
	s := &Server{config: config, db: db, lock: lock, connections: map[string]*connection{}, projects: map[string]*projectRecord{}, credentials: map[string]string{}, clients: map[string]protocolClient{}, operations: map[string]*operation{}, lastSeen: map[string]time.Time{}, closed: make(chan struct{})}
	rows, e := db.Query("SELECT data FROM records WHERE kind='connection'")
	if e != nil {
		s.Close()
		return nil, e
	}
	for rows.Next() {
		var raw string
		var c connection
		if e = rows.Scan(&raw); e != nil {
			break
		}
		if e = json.Unmarshal([]byte(raw), &c); e != nil {
			break
		}
		s.connections[c.ID] = &c
	}
	if e == nil {
		e = rows.Err()
	}
	rows.Close()
	if e != nil {
		s.Close()
		return nil, e
	}
	if e = s.loadProjects(); e != nil {
		s.Close()
		return nil, e
	}
	return s, nil
}
func (s *Server) Close() error {
	var err error
	s.closeOnce.Do(func() { close(s.closed); err = s.db.Close(); _ = s.lock.Close() })
	return err
}
func (s *Server) put(kind, id string, v any) error {
	return writeRecord(s.db, kind, id, v)
}
func (s *Server) get(kind, id string, v any) error {
	var b string
	e := s.db.QueryRow("SELECT data FROM records WHERE kind=? AND id=?", kind, id).Scan(&b)
	if e != nil {
		return e
	}
	return json.Unmarshal([]byte(b), v)
}
func jsonOut(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func problem(w http.ResponseWriter, status int, code string) {
	jsonOut(w, status, map[string]any{"error": map[string]any{"code": code, "message": strings.ReplaceAll(code, "_", " ")}})
}
func read(w http.ResponseWriter, r *http.Request, v any, limit int64) bool {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if d.Decode(v) != nil || d.Decode(new(any)) != io.EOF {
		problem(w, 400, "invalid_request")
		return false
	}
	return true
}
func (s *Server) project(w http.ResponseWriter, r *http.Request) *projectRecord {
	key := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if key == "" {
		key = r.Header.Get("x-api-key")
	}
	if key == "" {
		key = r.Header.Get("x-goog-api-key")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.projects[s.credentials[hash(key)]]
	if key == "" || p == nil || p.Revoked {
		problem(w, 401, "invalid_project_key")
		return nil
	}
	copy := *p
	return &copy
}
func (s *Server) connector(w http.ResponseWriter, r *http.Request) (*connection, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.connections[r.PathValue("connection")]
	if c == nil || !s.connectionActive(c) || !same(c.TokenHash, hash(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))) {
		problem(w, 401, "connector_denied")
		return nil, false
	}
	return c, true
}
func (s *Server) Handler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { jsonOut(w, 200, map[string]bool{"ok": true}) })
	m.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if s.db.Ping() != nil {
			problem(w, 503, "storage_unavailable")
			return
		}
		jsonOut(w, 200, map[string]bool{"ready": true})
	})
	m.HandleFunc("POST /relay/v1/enrollments", s.enroll)
	m.HandleFunc("POST /connector/v1/inspect", s.inspect)
	m.HandleFunc("POST /connector/v1/decline", s.decline)
	m.HandleFunc("POST /connector/v1/claim", s.claim)
	m.HandleFunc("POST /connector/v1/{connection}/poll", s.poll)
	m.HandleFunc("POST /connector/v1/{connection}/result", s.result)
	m.HandleFunc("GET /relay/v1/connections/{connection}", s.status)
	m.HandleFunc("DELETE /relay/v1/connections/{connection}", s.revoke)
	m.HandleFunc("POST /relay/v1/connections/{connection}/request", s.request)
	s.projectRoutes(m)
	s.facadeRoutes(m)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.Header.Get("Origin") != "" {
			problem(w, 403, "backend_only")
			return
		}
		m.ServeHTTP(w, r)
	})
}
func (s *Server) enroll(w http.ResponseWriter, r *http.Request) {
	p := s.project(w, r)
	if p == nil {
		return
	}
	var in struct {
		AppUser string `json:"appUser"`
	}
	if !read(w, r, &in, 4096) {
		return
	}
	if in.AppUser == "" || len(in.AppUser) > 200 {
		problem(w, 400, "invalid_account")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.projectActive(p.ID) {
		problem(w, 401, "invalid_project_key")
		return
	}
	_, _ = s.db.Exec("DELETE FROM records WHERE kind='ticket' AND updated < ?", time.Now().Add(-5*time.Minute).Unix())
	var count int
	if s.db.QueryRow("SELECT count(*) FROM records WHERE kind='connection' OR kind='ticket'").Scan(&count) != nil || count >= 4096 {
		problem(w, 429, "connection_limit")
		return
	}
	code := ID()
	id := ID()
	t := ticket{ProjectID: p.ID, AppUser: in.AppUser, CodeHash: hash(code), ConnectionID: id, Expires: time.Now().Add(5 * time.Minute)}
	if e := s.put("ticket", t.CodeHash, t); e != nil {
		problem(w, 500, "storage_error")
		return
	}
	jsonOut(w, 201, map[string]any{"code": code, "connectionId": id, "expiresAt": t.Expires, "appId": p.AppID, "name": p.Name, "origin": p.Origin})
}
func (s *Server) inspect(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Code string `json:"code"`
	}
	if !read(w, r, &in, 4096) {
		return
	}
	var t ticket
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.get("ticket", hash(in.Code), &t) != nil || time.Now().After(t.Expires) || t.Declined || !s.projectActive(t.ProjectID) {
		problem(w, 410, "pairing_expired")
		return
	}
	p := s.projects[t.ProjectID]
	jsonOut(w, 200, map[string]any{"appId": p.AppID, "name": p.Name, "origin": p.Origin, "appUser": t.AppUser, "connectionId": t.ConnectionID})
}
func (s *Server) claim(w http.ResponseWriter, r *http.Request) {
	var in struct{ Code, Invitation, Token string }
	if !read(w, r, &in, 16384) {
		return
	}
	var t ticket
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.get("ticket", hash(in.Code), &t) != nil || time.Now().After(t.Expires) || t.Declined || len(in.Token) != 64 || !s.projectActive(t.ProjectID) {
		problem(w, 410, "pairing_expired")
		return
	}
	inv, e := parseInvitation(in.Invitation)
	p := s.projects[t.ProjectID]
	if e != nil || inv.AppID != p.AppID || inv.AppUser != t.AppUser || inv.Origin != p.Origin {
		problem(w, 400, "identity_mismatch")
		return
	}
	private, e := newPrivate()
	if e != nil {
		problem(w, 500, "key_error")
		return
	}
	c := &connection{ID: t.ConnectionID, ProjectID: p.ID, AppUser: t.AppUser, TokenHash: hash(in.Token), Invitation: in.Invitation, Identity: inv, PrivateKey: private}
	// A code is consumed before saving the connection: uncertain enrollment is never replayed.
	if _, e = s.db.Exec("DELETE FROM records WHERE kind='ticket' AND id=?", t.CodeHash); e != nil {
		problem(w, 500, "storage_error")
		return
	}
	if e = s.put("connection", c.ID, c); e != nil {
		problem(w, 500, "storage_error")
		return
	}
	s.connections[c.ID] = c
	jsonOut(w, 201, map[string]string{"connectionId": c.ID})
}

// Declining requires the same single-use invitation as claiming. Persist a
// short-lived tombstone so the account backend can distinguish it from expiry.
func (s *Server) decline(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Code string `json:"code"`
	}
	if !read(w, r, &in, 4096) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var t ticket
	if s.get("ticket", hash(in.Code), &t) != nil || time.Now().After(t.Expires) || t.Declined || !s.projectActive(t.ProjectID) {
		problem(w, 410, "pairing_expired")
		return
	}
	t.Declined = true
	if s.put("ticket", t.CodeHash, t) != nil {
		problem(w, 500, "storage_error")
		return
	}
	jsonOut(w, 200, map[string]bool{"declined": true})
}
func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	p := s.project(w, r)
	if p == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.connections[r.PathValue("connection")]
	if c == nil {
		var raw string
		var t ticket
		err := s.db.QueryRow("SELECT data FROM records WHERE kind='ticket' AND json_extract(data,'$.ConnectionID')=?", r.PathValue("connection")).Scan(&raw)
		if err == nil && json.Unmarshal([]byte(raw), &t) == nil && t.ProjectID == p.ID && s.projectActive(p.ID) {
			if account := r.Header.Get("X-WDC-Account"); account != "" && account != t.AppUser {
				problem(w, 403, "account_mismatch")
				return
			}
			state := "pending"
			if time.Now().After(t.Expires) {
				state = "expired"
			} else if t.Declined {
				state = "declined"
			}
			jsonOut(w, 200, map[string]any{"connectionId": t.ConnectionID, "appUser": t.AppUser, "connected": false, "revoked": false, "enrollmentStatus": state})
			return
		}
	}
	if c == nil || c.ProjectID != p.ID || !s.projectActive(p.ID) {
		problem(w, 404, "connection_not_found")
		return
	}
	if account := r.Header.Get("X-WDC-Account"); account != "" && account != c.AppUser {
		problem(w, 403, "account_mismatch")
		return
	}
	jsonOut(w, 200, map[string]any{"connectionId": c.ID, "appUser": c.AppUser, "connected": !c.Revoked && time.Since(s.lastSeen[c.ID]) < Grace, "revoked": c.Revoked})
}
func (s *Server) revoke(w http.ResponseWriter, r *http.Request) {
	p := s.project(w, r)
	if p == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.connections[r.PathValue("connection")]
	if c == nil {
		// A pending enrollment can be disconnected before any desktop claims it.
		var raw string
		err := s.db.QueryRow("SELECT data FROM records WHERE kind='ticket' AND json_extract(data,'$.ConnectionID')=?", r.PathValue("connection")).Scan(&raw)
		var t ticket
		if err != nil || json.Unmarshal([]byte(raw), &t) != nil || t.ProjectID != p.ID || !s.projectActive(p.ID) {
			problem(w, 404, "connection_not_found")
			return
		}
		if account := r.Header.Get("X-WDC-Account"); account != "" && account != t.AppUser {
			problem(w, 403, "account_mismatch")
			return
		}
		if _, err = s.db.Exec("DELETE FROM records WHERE kind='ticket' AND id=?", t.CodeHash); err != nil {
			problem(w, 500, "storage_error")
			return
		}
		jsonOut(w, 200, map[string]bool{"revoked": true})
		return
	}
	if c.ProjectID != p.ID || !s.projectActive(p.ID) {
		problem(w, 404, "connection_not_found")
		return
	}
	if a := r.Header.Get("X-WDC-Account"); a != "" && a != c.AppUser {
		problem(w, 403, "account_mismatch")
		return
	}
	c.Revoked = true
	c.PrivateKey = ""
	c.Invitation = ""
	c.Identity.Secret = ""
	delete(s.clients, c.ID)
	if s.put("connection", c.ID, c) != nil {
		problem(w, 500, "storage_error")
		return
	}
	s.cancelConnection(c.ID)
	jsonOut(w, 200, map[string]bool{"revoked": true})
}
func (s *Server) poll(w http.ResponseWriter, r *http.Request) {
	c, ok := s.connector(w, r)
	if !ok {
		return
	}
	s.mu.Lock()
	s.lastSeen[c.ID] = time.Now()
	s.mu.Unlock()
	timer := time.NewTimer(25 * time.Second)
	defer timer.Stop()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		s.mu.Lock()
		packets := []Packet{}
		for _, op := range s.operations {
			if op.connection == c.ID && !op.claimed && time.Now().UnixMilli() < op.packet.Deadline {
				op.claimed = true
				packets = append(packets, op.packet)
				if len(packets) == 8 {
					break
				}
			}
		}
		revoked := !s.connectionActive(c)
		s.mu.Unlock()
		if revoked {
			problem(w, 401, "connector_revoked")
			return
		}
		if len(packets) > 0 {
			jsonOut(w, 200, map[string]any{"packets": packets})
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-s.closed:
			return
		case <-timer.C:
			jsonOut(w, 200, map[string]any{"packets": packets})
			return
		case <-tick.C:
		}
	}
}
func (s *Server) result(w http.ResponseWriter, r *http.Request) {
	c, ok := s.connector(w, r)
	if !ok {
		return
	}
	var in Result
	if !read(w, r, &in, 256<<10) {
		return
	}
	s.mu.Lock()
	op := s.operations[in.ID]
	if !s.connectionActive(c) || op == nil || op.connection != c.ID || !op.claimed || op.sequence != in.Sequence || time.Now().UnixMilli() > op.packet.Deadline {
		s.mu.Unlock()
		problem(w, 409, "operation_expired_or_replayed")
		return
	}
	if op.size+len(in.Body) > 64<<20 {
		s.mu.Unlock()
		problem(w, 413, "response_limit")
		return
	}
	op.size += len(in.Body)
	op.sequence++
	s.lastSeen[c.ID] = time.Now()
	s.mu.Unlock()
	select {
	case op.results <- in:
		jsonOut(w, 200, map[string]bool{"accepted": true})
	case <-op.done:
		problem(w, 410, "operation_expired")
	case <-r.Context().Done():
	}
}
func (s *Server) tunnel(ctx context.Context, c *connection, path string, body []byte) (*http.Response, error) {
	if path != "/v2/channel" && path != "/v2/request" {
		return nil, errors.New("control tunneling denied")
	}
	ctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	id := ID()
	op := &operation{packet: Packet{ID: id, Path: path, Body: string(body), Deadline: time.Now().Add(40 * time.Second).UnixMilli()}, connection: c.ID, results: make(chan Result, 8), done: make(chan struct{}), cancel: cancel}
	s.mu.Lock()
	if !s.connectionActive(c) || time.Since(s.lastSeen[c.ID]) >= Grace {
		s.mu.Unlock()
		cancel()
		return nil, errors.New("device_offline")
	}
	count := 0
	for _, active := range s.operations {
		if active.connection == c.ID {
			count++
		}
	}
	if len(s.operations) >= 64 || count >= 16 {
		s.mu.Unlock()
		cancel()
		return nil, errors.New("operation_limit")
	}
	s.operations[id] = op
	s.mu.Unlock()
	pr, pw := io.Pipe()
	go func() {
		select {
		case <-ctx.Done():
			_ = pw.CloseWithError(ctx.Err())
		case <-op.done:
		case <-s.closed:
			_ = pw.CloseWithError(errors.New("relay_stopped"))
		}
	}()
	go func() {
		defer cancel()
		defer close(op.done)
		defer func() { s.mu.Lock(); delete(s.operations, id); s.mu.Unlock() }()
		for {
			select {
			case result := <-op.results:
				if result.Body != "" {
					if _, e := io.WriteString(pw, result.Body); e != nil {
						return
					}
				}
				if result.End {
					_ = pw.Close()
					return
				}
			case <-ctx.Done():
				_ = pw.CloseWithError(ctx.Err())
				return
			case <-s.closed:
				_ = pw.CloseWithError(errors.New("relay_stopped"))
				return
			}
		}
	}()
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/x-ndjson"}}, Body: pr}, nil
}
func (s *Server) client(projectID, id, account string) (protocolClient, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.connections[id]
	if c == nil || c.ProjectID != projectID || !s.connectionActive(c) {
		return nil, errors.New("connection_unavailable")
	}
	if account != "" && account != c.AppUser {
		return nil, errors.New("account_mismatch")
	}
	if client := s.clients[id]; client != nil {
		return client, nil
	}
	copy := *c
	client, e := makeV2Client(&copy, func(ctx context.Context, path string, body []byte) (*http.Response, error) {
		return s.tunnel(ctx, c, path, body)
	}, func() error {
		s.mu.Lock()
		defer s.mu.Unlock()
		c.PairingID = copy.PairingID
		c.Invitation = ""
		c.Identity.Secret = ""
		return s.put("connection", c.ID, c)
	})
	if e != nil {
		return nil, e
	}
	s.clients[id] = client
	return client, nil
}

type logicalRequest struct {
	ID       string `json:"requestId"`
	Deadline int64  `json:"deadline"`
	Method   string `json:"method"`
	Path     string `json:"path"`
	Body     string `json:"body"`
	Lease    string `json:"lease"`
}

func (s *Server) request(w http.ResponseWriter, r *http.Request) {
	p := s.project(w, r)
	if p == nil {
		return
	}
	var in logicalRequest
	if !read(w, r, &in, 128<<10) {
		return
	}
	if in.Deadline <= time.Now().UnixMilli() || in.Deadline > time.Now().Add(time.Minute).UnixMilli() {
		problem(w, 408, "request_expired")
		return
	}
	if _, err := hex.DecodeString(in.ID); err != nil || len(in.ID) != 64 || len(in.Body) > 40<<10 {
		problem(w, 400, "invalid_request")
		return
	}
	if !strings.HasPrefix(in.Path, "/v2/") || strings.ContainsAny(in.Path, "?#%\\") || strings.Contains(in.Path, "..") || in.Path == "/v2/channel" || in.Path == "/v2/request" || in.Path == "/v2/pairings/claim" {
		problem(w, 403, "resource_denied")
		return
	}
	if in.Method != "GET" && in.Method != "POST" && in.Method != "PUT" && in.Method != "DELETE" {
		problem(w, 400, "invalid_method")
		return
	}
	client, e := s.client(p.ID, r.PathValue("connection"), r.Header.Get("X-WDC-Account"))
	if e != nil {
		problem(w, 403, e.Error())
		return
	}
	// Reserve an immutable receipt before sending any mutating operation.
	if in.Method != "GET" {
		key := hash(r.PathValue("connection") + ":" + in.ID)
		b, _ := json.Marshal(in)
		if e = s.reserve("receipt", key, hash(string(b))); e != nil {
			problem(w, 409, "operation_consumed")
			return
		}
	}
	resp, e := client.call(r.Context(), in.Method, in.Path, in.Body, in.Lease, "")
	if e != nil {
		problem(w, 503, "submission_unknown")
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(resp.StatusCode)
	b := make([]byte, 24<<10)
	for {
		n, e := resp.Body.Read(b)
		if n > 0 {
			if _, err := w.Write(b[:n]); err != nil {
				return
			}
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
		if e != nil {
			return
		}
	}
}

// Receipts contain hashes only. Bound their count instead of deleting replay protection.
func (s *Server) reserve(kind, key, digest string) error {
	// Transport receipts may retire only after their mandatory deadline is stale.
	// Facade inference keys stay durable, because provider SDK calls have no deadline envelope.
	if _, err := s.db.Exec("DELETE FROM records WHERE kind='receipt' AND updated < ?", time.Now().Add(-2*time.Minute).Unix()); err != nil {
		return err
	}
	var count int
	if err := s.db.QueryRow("SELECT count(*) FROM records WHERE kind IN ('receipt','facade')").Scan(&count); err != nil {
		return err
	}
	if count >= 1000000 {
		return errors.New("receipt_limit")
	}
	_, err := s.db.Exec("INSERT INTO records VALUES(?,?,?,?)", kind, key, digest, time.Now().Unix())
	return err
}
func (s *Server) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return fmt.Sprintf("WDC relay (%d registered projects)", len(s.projects))
}
