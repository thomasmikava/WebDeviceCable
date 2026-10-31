package relay

import (
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const maxProjects = 256

// Project identity is immutable: changing an origin requires a new app ID and
// fresh desktop consent. Only credential hashes are persisted.
type projectRecord struct {
	ID             string    `json:"id"`
	AppID          string    `json:"appId"`
	Name           string    `json:"name"`
	Origin         string    `json:"origin"`
	CreatedAt      time.Time `json:"createdAt"`
	Revoked        bool      `json:"revoked"`
	CredentialHash string    `json:"credentialHash"`
}

type projectView struct {
	ID        string    `json:"id"`
	AppID     string    `json:"appId"`
	Name      string    `json:"name"`
	Origin    string    `json:"origin"`
	CreatedAt time.Time `json:"createdAt"`
	Revoked   bool      `json:"revoked"`
}

func (p *projectRecord) view() projectView {
	return projectView{p.ID, p.AppID, p.Name, p.Origin, p.CreatedAt, p.Revoked}
}

func validIdentity(appID, name, origin string) bool {
	for _, value := range []string{appID, name} {
		if len(value) < 1 || len(value) > 100 || strings.TrimSpace(value) != value || strings.ContainsFunc(value, unicode.IsControl) {
			return false
		}
	}
	u, err := url.Parse(origin)
	if err != nil || len(origin) > 2048 || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" || u.String() != origin {
		return false
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return false
		}
	}
	ip := net.ParseIP(u.Hostname())
	return u.Scheme == "https" || (u.Scheme == "http" && (u.Hostname() == "localhost" || ip != nil && ip.IsLoopback()))
}

func validateConfig(c Config) error {
	if len(c.AdminKey) < 32 {
		return errors.New("admin key (32+ characters) is required; register projects through the management API")
	}
	return nil
}

type recordWriter interface {
	Exec(string, ...any) (sql.Result, error)
}

func writeRecord(db recordWriter, kind, id string, value any) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = db.Exec("INSERT INTO records VALUES(?,?,?,?) ON CONFLICT(kind,id) DO UPDATE SET data=excluded.data,updated=excluded.updated", kind, id, string(b), time.Now().Unix())
	return err
}

// Load persisted projects without creating identities or changing ownership.
func (s *Server) loadProjects() error {
	rows, err := s.db.Query("SELECT id,data FROM records WHERE kind='project'")
	if err != nil {
		return err
	}
	appIDs, digests := map[string]bool{}, map[string]bool{}
	for rows.Next() {
		var id, raw string
		var p projectRecord
		if err = rows.Scan(&id, &raw); err != nil {
			break
		}
		if err = json.Unmarshal([]byte(raw), &p); err != nil {
			break
		}
		_, digestErr := hex.DecodeString(p.CredentialHash)
		if id != p.ID || len(id) != 64 || len(p.CredentialHash) != 64 || digestErr != nil || !validIdentity(p.AppID, p.Name, p.Origin) || appIDs[p.AppID] || digests[p.CredentialHash] {
			err = errors.New("invalid project registry")
			break
		}
		if same(hash(s.config.AdminKey), p.CredentialHash) {
			err = errors.New("admin and project credentials must differ")
			break
		}
		s.projects[id] = &p
		appIDs[p.AppID], digests[p.CredentialHash] = true, true
		if !p.Revoked {
			s.credentials[p.CredentialHash] = id
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	for _, c := range s.connections {
		if c.ProjectID == "" {
			return errors.New("unscoped relay connection: preserve this data directory and register/re-pair projects in a new directory")
		}
		p := s.projects[c.ProjectID]
		if p == nil || c.Identity.AppID != p.AppID || c.Identity.Origin != p.Origin {
			return errors.New("connection project identity mismatch")
		}
	}
	rows, err = s.db.Query("SELECT data FROM records WHERE kind='ticket'")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		var t ticket
		if err = rows.Scan(&raw); err != nil {
			return err
		}
		if err = json.Unmarshal([]byte(raw), &t); err != nil {
			return err
		}
		if t.ProjectID == "" {
			return errors.New("unscoped relay enrollment: preserve this data directory and register/re-pair projects in a new directory")
		}
		if s.projects[t.ProjectID] == nil {
			return errors.New("enrollment project is missing")
		}
	}
	return rows.Err()
}

// These helpers require s.mu. Project revocation applies to cached clients too.
func (s *Server) projectActive(id string) bool {
	p := s.projects[id]
	return p != nil && !p.Revoked
}
func (s *Server) connectionActive(c *connection) bool {
	return !c.Revoked && s.projectActive(c.ProjectID)
}
func (s *Server) cancelConnection(id string) {
	for _, op := range s.operations {
		if op.connection == id && op.cancel != nil {
			op.cancel()
		}
	}
}

func (s *Server) admin(w http.ResponseWriter, r *http.Request) bool {
	if !same(r.Header.Get("Authorization"), "Bearer "+s.config.AdminKey) {
		problem(w, 401, "invalid_admin_key")
		return false
	}
	return true
}

func (s *Server) projectRoutes(m *http.ServeMux) {
	m.HandleFunc("POST /relay/admin/v1/projects", s.createProject)
	m.HandleFunc("GET /relay/admin/v1/projects", s.listProjects)
	m.HandleFunc("GET /relay/admin/v1/projects/{project}", s.getProject)
	m.HandleFunc("POST /relay/admin/v1/projects/{project}/rotate-key", s.rotateProjectKey)
	m.HandleFunc("DELETE /relay/admin/v1/projects/{project}", s.revokeProject)
}

func (s *Server) createProject(w http.ResponseWriter, r *http.Request) {
	if !s.admin(w, r) {
		return
	}
	var in struct{ AppID, Name, Origin string }
	if !read(w, r, &in, 4096) {
		return
	}
	if !validIdentity(in.AppID, in.Name, in.Origin) {
		problem(w, 400, "invalid_project_identity")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.projects {
		if p.AppID == in.AppID {
			problem(w, 409, "app_id_exists")
			return
		}
	}
	if len(s.projects) >= maxProjects {
		problem(w, 429, "project_limit")
		return
	}
	key := "wdcp_" + ID()
	p := &projectRecord{ID: ID(), AppID: in.AppID, Name: in.Name, Origin: in.Origin, CreatedAt: time.Now().UTC(), CredentialHash: hash(key)}
	if s.put("project", p.ID, p) != nil {
		problem(w, 500, "storage_error")
		return
	}
	s.projects[p.ID] = p
	s.credentials[p.CredentialHash] = p.ID
	jsonOut(w, 201, map[string]any{"project": p.view(), "apiKey": key})
}

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	if !s.admin(w, r) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	projects := make([]projectView, 0, len(s.projects))
	for _, p := range s.projects {
		projects = append(projects, p.view())
	}
	sort.Slice(projects, func(i, j int) bool { return projects[i].AppID < projects[j].AppID })
	jsonOut(w, 200, map[string]any{"projects": projects})
}

func (s *Server) getProject(w http.ResponseWriter, r *http.Request) {
	if !s.admin(w, r) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.projects[r.PathValue("project")]
	if p == nil {
		problem(w, 404, "project_not_found")
		return
	}
	jsonOut(w, 200, p.view())
}

func (s *Server) rotateProjectKey(w http.ResponseWriter, r *http.Request) {
	if !s.admin(w, r) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.projects[r.PathValue("project")]
	if p == nil || p.Revoked {
		problem(w, 404, "project_not_found")
		return
	}
	key := "wdcp_" + ID()
	copy := *p
	copy.CredentialHash = hash(key)
	if s.put("project", p.ID, &copy) != nil {
		problem(w, 500, "storage_error")
		return
	}
	delete(s.credentials, p.CredentialHash)
	*p = copy
	s.credentials[p.CredentialHash] = p.ID
	jsonOut(w, 200, map[string]any{"project": p.view(), "apiKey": key})
}

func (s *Server) revokeProject(w http.ResponseWriter, r *http.Request) {
	if !s.admin(w, r) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.projects[r.PathValue("project")]
	if p == nil {
		problem(w, 404, "project_not_found")
		return
	}
	copy := *p
	copy.Revoked = true
	tx, err := s.db.Begin()
	if err != nil {
		problem(w, 500, "storage_error")
		return
	}
	defer tx.Rollback()
	if err = writeRecord(tx, "project", p.ID, &copy); err == nil {
		_, err = tx.Exec("DELETE FROM records WHERE kind='ticket' AND json_extract(data,'$.ProjectID')=?", p.ID)
	}
	updated := map[string]connection{}
	for id, c := range s.connections {
		if err != nil || c.ProjectID != p.ID {
			continue
		}
		v := *c
		v.Revoked, v.PrivateKey, v.Invitation, v.Identity.Secret = true, "", "", ""
		updated[id] = v
		err = writeRecord(tx, "connection", id, &v)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		problem(w, 500, "storage_error")
		return
	}
	delete(s.credentials, p.CredentialHash)
	*p = copy
	for id, c := range updated {
		// ID/project/account and the public identity are immutable and are read
		// by active tunnels outside the lock. Only replace revocable fields.
		live := s.connections[id]
		live.Revoked, live.PrivateKey, live.Invitation, live.Identity.Secret = c.Revoked, c.PrivateKey, c.Invitation, c.Identity.Secret
		delete(s.clients, id)
		s.cancelConnection(id)
	}
	jsonOut(w, 200, p.view())
}
