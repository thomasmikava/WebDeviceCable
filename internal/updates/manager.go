package updates

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type Status struct {
	Version     string    `json:"version"`
	Official    bool      `json:"official"`
	Community   bool      `json:"community,omitempty"`
	Supported   bool      `json:"supported"`
	Automatic   bool      `json:"automatic"`
	Stage       string    `json:"stage"`
	Available   string    `json:"available,omitempty"`
	Downloaded  int64     `json:"downloaded,omitempty"`
	Size        int64     `json:"size,omitempty"`
	LastChecked time.Time `json:"lastChecked,omitempty"`
	Error       string    `json:"error,omitempty"`
}
type preferences struct {
	Automatic   *bool     `json:"automatic,omitempty"`
	LastChecked time.Time `json:"lastChecked"`
	Highest     string    `json:"highest,omitempty"`
}
type Hooks struct {
	Read    func(string, int64) ([]byte, error)
	Write   func(string, []byte) error
	Prepare func(string) error
	// Begin atomically closes admission only when idle, or after explicit cancellation.
	Begin   func(force bool) (bool, error)
	End     func()
	Install func(Plan) error
}
type Plan struct {
	Metadata []byte   `json:"metadata"`
	Release  Release  `json:"release"`
	Artifact Artifact `json:"artifact"`
	Archive  string   `json:"archive"`
	Staged   string   `json:"staged"`
	Native   bool     `json:"native"`
}
type NativeCommand struct {
	Action  string `json:"action"`
	Version string `json:"version,omitempty"`
	URL     string `json:"url,omitempty"`
	Size    int64  `json:"size,omitempty"`
	Manual  bool   `json:"manual"`
}
type Manager struct {
	mu           sync.Mutex
	build        Build
	dir          string
	hooks        Hooks
	client       *http.Client
	feed         string
	prefs        preferences
	status       Status
	plan         *Plan
	busy         bool
	requested    bool
	force        bool
	ready        bool
	nativeAction string
	cancel       context.CancelFunc
	done         chan struct{}
	ctx          context.Context
	workers      sync.WaitGroup
	lastAttempt  time.Time
}

func New(b Build, dir string, h Hooks) (*Manager, error) {
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{build: b, dir: dir, hooks: h, feed: Feed, ctx: ctx, cancel: cancel, done: make(chan struct{})}
	m.client = &http.Client{Timeout: 10 * time.Minute, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) > 5 || r.URL.Scheme != "https" || r.URL.User != nil {
			return errors.New("unsafe update redirect")
		}
		switch r.URL.Host {
		case "github.com", "release-assets.githubusercontent.com", "objects.githubusercontent.com":
			return nil
		}
		return errors.New("unexpected update host")
	}}
	key, e := base64.StdEncoding.DecodeString(b.PublicKey)
	supported := b.Official && !b.Community && e == nil && len(key) == 32
	if b.OS == "darwin" && !b.Native {
		supported = false
	}
	if _, e = Version(b.Version); e != nil {
		supported = false
	}
	m.status = Status{Version: b.Version, Official: b.Official && !b.Community, Community: b.Community, Supported: supported, Automatic: supported, Stage: "idle"}
	disable := func() (*Manager, error) {
		m.status.Supported = false
		m.status.Automatic = false
		m.status.Stage = "error"
		m.status.Error = "Updates are paused because their local settings could not be read. Your current installation still works."
		return m, nil
	}
	if !supported {
		m.status.Stage = "development"
	}
	if b.Community {
		m.status.Stage = "community"
	}
	if b.Official && !b.Community && b.OS == "darwin" && !b.Native {
		m.status.Stage = "application"
	}
	if e = h.Prepare(dir); e != nil {
		return disable()
	}
	data, e := h.Read(filepath.Join(dir, "preferences.json"), 8192)
	if e == nil {
		if strict(data, &m.prefs) != nil {
			return disable()
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return disable()
	}
	if m.prefs.Highest != "" {
		if _, e := Version(m.prefs.Highest); e != nil {
			return disable()
		}
	}
	if m.prefs.Automatic != nil {
		m.status.Automatic = supported && *m.prefs.Automatic
	}
	m.status.LastChecked = m.prefs.LastChecked
	return m, nil
}
func (m *Manager) Start() {
	go func() {
		defer close(m.done)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			m.step()
			select {
			case <-m.ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}
func (m *Manager) Close()         { m.cancel(); <-m.done; m.workers.Wait() }
func (m *Manager) Status() Status { m.mu.Lock(); defer m.mu.Unlock(); return m.status }
func (m *Manager) saveLocked() error {
	b, e := json.Marshal(m.prefs)
	if e != nil {
		return e
	}
	return m.hooks.Write(filepath.Join(m.dir, "preferences.json"), b)
}
func (m *Manager) SetAutomatic(on bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.status.Stage == "installing" {
		return errors.New("installation has already started; change this setting after restart")
	}
	if !m.status.Supported {
		return errors.New("automatic updates are available in official installations")
	}
	old := m.prefs.Automatic
	m.prefs.Automatic = &on
	if e := m.saveLocked(); e != nil {
		m.prefs.Automatic = old
		return e
	}
	m.status.Automatic = on
	if !on && !m.force {
		m.requested = false
	}
	return nil
}
func (m *Manager) fail(e error) {
	m.mu.Lock()
	m.status.Stage = "error"
	m.status.Error = e.Error()
	m.busy = false
	m.requested = false
	m.nativeAction = ""
	m.mu.Unlock()
	m.hooks.End()
}
func (m *Manager) Check() error {
	m.mu.Lock()
	if !m.status.Supported {
		m.mu.Unlock()
		return errors.New("this development build does not install official updates")
	}
	if m.busy || m.status.Stage == "installing" {
		m.mu.Unlock()
		return errors.New("an update operation is already in progress")
	}
	m.busy = true
	m.lastAttempt = time.Now()
	m.status.Stage = "checking"
	m.status.Error = ""
	m.workers.Add(1)
	m.mu.Unlock()
	go func() {
		defer m.workers.Done()
		if e := m.check(); e != nil {
			m.fail(e)
		}
	}()
	return nil
}
func (m *Manager) check() error {
	ctx, cancel := context.WithTimeout(m.ctx, 30*time.Second)
	defer cancel()
	req, e := http.NewRequestWithContext(ctx, "GET", m.feed, nil)
	if e != nil {
		return e
	}
	req.Header.Set("User-Agent", "WebDeviceCable/"+m.build.Version)
	req.Header.Set("Accept", "application/json")
	response, e := m.client.Do(req)
	if e != nil {
		return errors.New("could not check for updates; try again later")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return fmt.Errorf("update service returned HTTP %d", response.StatusCode)
	}
	raw, e := io.ReadAll(io.LimitReader(response.Body, (128<<10)+1))
	if e != nil {
		return e
	}
	release, e := Verify(raw, m.build.PublicKey)
	if e != nil {
		return e
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.prefs.Highest != "" && Newer(m.prefs.Highest, release.Version) {
		return errors.New("older release metadata was rejected")
	}
	old := m.prefs
	m.prefs.LastChecked = time.Now().UTC()
	m.prefs.Highest = release.Version
	if e = m.saveLocked(); e != nil {
		m.prefs = old
		return e
	}
	m.status.LastChecked = m.prefs.LastChecked
	m.status.Available = ""
	m.status.Downloaded = 0
	m.status.Size = 0
	m.status.Error = ""
	m.busy = false
	m.plan = nil
	m.ready = false
	m.force = false
	m.requested = false
	if !Newer(release.Version, m.build.Version) {
		m.status.Stage = "current"
		return nil
	}
	artifact, e := release.Target(m.build)
	if e != nil {
		return e
	}
	m.status.Available = release.Version
	m.status.Size = artifact.Size
	if release.StateVersion != 1 || release.ProtocolVersion != 2 {
		m.status.Stage = "manual"
		return nil
	}
	m.plan = &Plan{Release: release, Artifact: artifact, Native: m.build.Native, Metadata: raw}
	m.status.Stage = "available"
	return nil
}
func (m *Manager) Install(force bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.plan == nil || m.status.Stage == "manual" || m.status.Stage == "installing" || m.status.Stage == "error" || m.status.Stage == "checking" {
		return errors.New("check for a compatible update first")
	}
	m.requested = true
	m.force = force
	return nil
}
func (m *Manager) step() {
	m.mu.Lock()
	if m.busy || !m.status.Supported || m.status.Stage == "installing" {
		m.mu.Unlock()
		return
	}
	if m.plan != nil && (m.requested || m.status.Automatic) && m.status.Stage != "error" && (m.status.Stage != "authorization" || m.requested) {
		if !m.ready {
			m.busy = true
			m.status.Stage = "downloading"
			p := *m.plan
			m.workers.Add(1)
			m.mu.Unlock()
			go func() {
				defer m.workers.Done()
				if e := m.download(p); e != nil {
					m.fail(e)
				}
			}()
			return
		}
		force := m.force
		m.busy = true
		m.mu.Unlock()
		idle, e := m.hooks.Begin(force)
		if e != nil {
			m.fail(e)
			return
		}
		m.mu.Lock()
		m.busy = false
		if !m.requested && !m.status.Automatic {
			m.mu.Unlock()
			m.hooks.End()
			return
		}
		if !idle {
			m.status.Stage = "waiting"
			m.mu.Unlock()
			return
		}
		m.status.Stage = "installing"
		p := *m.plan
		if p.Native {
			m.nativeAction = "install"
			m.mu.Unlock()
			return
		}
		m.mu.Unlock()
		if e = m.hooks.Install(p); e != nil {
			m.fail(e)
		}
		return
	}
	due := m.status.Automatic && (m.prefs.LastChecked.IsZero() || time.Since(m.prefs.LastChecked) > 24*time.Hour)
	// Back off errors and unsupported migrations, without affecting ordinary execution.
	if time.Since(m.lastAttempt) < time.Hour {
		due = false
	}
	m.mu.Unlock()
	if due {
		_ = m.Check()
	}
}
func (m *Manager) download(p Plan) error {
	dir := filepath.Join(m.dir, "download-"+p.Release.Version)
	if e := m.hooks.Prepare(dir); e != nil {
		return e
	}
	m.prune(dir)
	archive := filepath.Join(dir, "release.archive")
	if i, e := os.Lstat(archive); e == nil {
		if !i.Mode().IsRegular() {
			return errors.New("unsafe staged update")
		}
		if e = os.Remove(archive); e != nil {
			return e
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	f, e := os.OpenFile(archive, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	success := false
	defer func() {
		f.Close()
		if !success {
			os.Remove(archive)
		}
	}()
	req, e := http.NewRequestWithContext(m.ctx, "GET", p.Artifact.URL, nil)
	if e != nil {
		return e
	}
	req.Header.Set("User-Agent", "WebDeviceCable/"+m.build.Version)
	r, e := m.client.Do(req)
	if e != nil {
		return errors.New("update download failed; your current installation is unchanged")
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		return errors.New("update download unavailable")
	}
	h := sha256.New()
	n, e := io.Copy(io.MultiWriter(f, h, downloadProgress(func(n int) { m.mu.Lock(); m.status.Downloaded += int64(n); m.mu.Unlock() })), io.LimitReader(r.Body, p.Artifact.Size+1))
	if e != nil {
		return e
	}
	if n != p.Artifact.Size || hex.EncodeToString(h.Sum(nil)) != p.Artifact.SHA256 {
		return errors.New("update size or hash verification failed")
	}
	if e = f.Sync(); e != nil {
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	staged := filepath.Join(dir, "files")
	if info, err := os.Lstat(staged); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("unsafe update staging directory")
		}
		if err = os.RemoveAll(staged); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if !p.Native {
		if e = Extract(archive, staged, m.build.OS == "windows"); e != nil {
			return e
		}
	}
	p.Archive = archive
	p.Staged = staged
	m.mu.Lock()
	m.plan = &p
	m.ready = true
	m.busy = false
	m.status.Stage = "ready"
	m.status.Downloaded = n
	m.mu.Unlock()
	success = true
	return nil
}

// Keep the current staging area and one prior download, plus two installer
// recovery directories. Ignore unknown files and never follow directory links.
func (m *Manager) prune(current string) {
	entries, e := os.ReadDir(m.dir)
	if e != nil {
		return
	}
	for _, prefix := range []string{"download-", "install-"} {
		var owned []os.DirEntry
		for _, entry := range entries {
			if !entry.IsDir() || !strings.HasPrefix(entry.Name(), prefix) {
				continue
			}
			if prefix == "download-" {
				if _, e := Version(strings.TrimPrefix(entry.Name(), prefix)); e != nil {
					continue
				}
			}
			owned = append(owned, entry)
		}
		sort.Slice(owned, func(i, j int) bool {
			a, e := owned[i].Info()
			b, f := owned[j].Info()
			return e == nil && f == nil && a.ModTime().After(b.ModTime())
		})
		kept := 0
		if prefix == "download-" {
			kept = 1
		}
		for _, entry := range owned {
			path := filepath.Join(m.dir, entry.Name())
			if path == current {
				continue
			}
			if kept < 2 {
				kept++
				continue
			}
			_ = os.RemoveAll(path)
		}
	}
}

type downloadProgress func(int)

func (p downloadProgress) Write(b []byte) (int, error) { p(len(b)); return len(b), nil }

// Native commands are read through root-authenticated local IPC by the macOS launcher.
func (m *Manager) NativePoll() NativeCommand {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := NativeCommand{Action: m.nativeAction}
	if m.plan != nil {
		c.Version = m.plan.Release.Version
		c.URL = m.plan.Artifact.URL
		c.Size = m.plan.Artifact.Size
		c.Manual = m.requested
	}

	return c
}
func (m *Manager) NativeReport(version, stage, detail string) error {
	m.mu.Lock()
	if !m.build.Native || m.plan == nil || m.plan.Release.Version != version {
		m.mu.Unlock()
		return errors.New("native updater version mismatch")
	}
	switch stage {
	case "ready":
		m.ready = true
		m.busy = false
		m.status.Stage = "ready"
	case "authorization":
		m.ready = true
		m.busy = false
		m.requested = false
		m.status.Stage = "authorization"
		m.nativeAction = ""
		m.mu.Unlock()
		m.hooks.End()
		return nil
	case "error":
		m.mu.Unlock()
		m.fail(errors.New("macOS update could not complete; open Updates to retry"))
		return nil
	default:
		m.mu.Unlock()
		return errors.New("invalid native update state")
	}
	m.mu.Unlock()
	return nil
}
func (m *Manager) InstallationFailed() {
	m.fail(errors.New("update installation failed; your private data was preserved. Retry or reinstall the verified release"))
}
