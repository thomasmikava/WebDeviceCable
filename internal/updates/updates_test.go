package updates

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func signedRelease(t *testing.T, r Release, key ed25519.PrivateKey) []byte {
	t.Helper()
	p, e := json.Marshal(r)
	if e != nil {
		t.Fatal(e)
	}
	b, e := json.Marshal(Envelope{Payload: base64.StdEncoding.EncodeToString(p), Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(key, Message(p)))})
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func sampleRelease(data []byte) Release {
	return Release{Format: 1, Version: "0.2.0", StateVersion: 1, ProtocolVersion: 2, Artifacts: []Artifact{{OS: "linux", Arch: "amd64", URL: "https://github.com/" + Repository + "/releases/download/v0.2.0/WebDeviceCable-0.2.0-linux-amd64.tar.gz", Size: int64(len(data)), SHA256: Hash(data)}}}
}
func TestReleaseTrustAndBounds(t *testing.T) {
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	trust := base64.StdEncoding.EncodeToString(pub)
	r := sampleRelease([]byte("data"))
	raw := signedRelease(t, r, key)
	if _, e := Verify(raw, trust); e != nil {
		t.Fatal(e)
	}
	other, _, _ := ed25519.GenerateKey(rand.Reader)
	if _, e := Verify(raw, base64.StdEncoding.EncodeToString(other)); e == nil {
		t.Fatal("accepted wrong key")
	}
	var env Envelope
	_ = json.Unmarshal(raw, &env)
	env.Payload = base64.StdEncoding.EncodeToString([]byte(`{"version":"999.0.0"}`))
	tampered, _ := json.Marshal(env)
	if _, e := Verify(tampered, trust); e == nil {
		t.Fatal("accepted tampering")
	}
	for _, bad := range []string{"https://evil.example/app.zip", "http://github.com/" + Repository + "/releases/download/v0.2.0/app.zip", "https://github.com/" + Repository + "/releases/download/v0.2.0/../app.zip", "https://github.com/" + Repository + "/releases/download/v0.2.0/app.zip?token=x", "https://github.com/" + Repository + "/releases/download/v0.2.0/%2e%2e.zip"} {
		r.Artifacts[0].URL = bad
		if _, e := Verify(signedRelease(t, r, key), trust); e == nil {
			t.Fatalf("accepted asset %s", bad)
		}
	}
	r = sampleRelease([]byte("data"))
	r.Artifacts = append(r.Artifacts, r.Artifacts[0])
	if _, e := Verify(signedRelease(t, r, key), trust); e == nil {
		t.Fatal("accepted duplicate target")
	}
	if _, e := Verify(make([]byte, 129<<10), trust); e == nil {
		t.Fatal("accepted oversized metadata")
	}
	for _, v := range []string{"1.0", "01.0.0", "1.0.0-beta", "1.0.0+build", "99999999999999999.0.0"} {
		if _, e := Version(v); e == nil {
			t.Fatalf("accepted version %q", v)
		}
	}
	if !Newer("1.0.0", "0.99.99") || Newer("0.1.0", "0.1.0") || Newer("0.1.0", "0.2.0") {
		t.Fatal("wrong version ordering")
	}
}
func tarBytes(t *testing.T, name string, kind byte, data []byte) []byte {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	w := tar.NewWriter(gz)
	if e := w.WriteHeader(&tar.Header{Name: name, Mode: 0755, Typeflag: kind, Size: int64(len(data)), Linkname: "../../escape"}); e != nil {
		t.Fatal(e)
	}
	if len(data) > 0 {
		if _, e := w.Write(data); e != nil {
			t.Fatal(e)
		}
	}
	if e := w.Close(); e != nil {
		t.Fatal(e)
	}
	if e := gz.Close(); e != nil {
		t.Fatal(e)
	}
	return b.Bytes()
}
func TestArchiveBoundariesAndExactBytes(t *testing.T) {
	data := []byte{0, 255, 13, 10, 128, 1}
	dir := t.TempDir()
	archive := filepath.Join(dir, "update.tgz")
	if e := os.WriteFile(archive, tarBytes(t, "package/WebDeviceCable", tar.TypeReg, data), 0600); e != nil {
		t.Fatal(e)
	}
	out := filepath.Join(dir, "valid")
	if e := Extract(archive, out, false); e != nil {
		t.Fatal(e)
	}
	got, _ := os.ReadFile(filepath.Join(out, "WebDeviceCable"))
	if !bytes.Equal(got, data) {
		t.Fatal("file bytes changed")
	}
	for i, name := range []string{"package/../../escape", "/tmp/escape", "package/C:/escape", "package/dir\\escape", "package/NUL", "package/CON.txt", "package/name.", "../escape"} {
		t.Run(name, func(t *testing.T) {
			_ = os.WriteFile(archive, tarBytes(t, name, tar.TypeReg, data), 0600)
			if e := Extract(archive, filepath.Join(dir, "bad"+strings.Repeat("x", i)), false); e == nil {
				t.Fatal("accepted unsafe path")
			}
		})
	}
	_ = os.WriteFile(archive, tarBytes(t, "package/link", tar.TypeSymlink, nil), 0600)
	if e := Extract(archive, filepath.Join(dir, "link"), false); e == nil {
		t.Fatal("accepted symlink")
	}
	var z bytes.Buffer
	zw := zip.NewWriter(&z)
	h := &zip.FileHeader{Name: "package/link"}
	h.SetMode(os.ModeSymlink | 0777)
	entry, _ := zw.CreateHeader(h)
	_, _ = entry.Write([]byte("../../escape"))
	_ = zw.Close()
	_ = os.WriteFile(archive, z.Bytes(), 0600)
	if e := Extract(archive, filepath.Join(dir, "ziplink"), true); e == nil {
		t.Fatal("accepted zip symlink")
	}
}

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func managerFixture(t *testing.T, system string) (*Manager, *atomic.Bool, *atomic.Int32, *[]byte) {
	t.Helper()
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	archive := tarBytes(t, "WebDeviceCable/WebDeviceCable", tar.TypeReg, []byte("new version"))
	r := sampleRelease(archive)
	r.Artifacts[0].OS = system
	metadata := signedRelease(t, r, key)
	busy := &atomic.Bool{}
	installed := &atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("Authorization") != "" {
			t.Error("sent credentials to update service")
		}
		if req.URL.Path == "/feed" {
			_, _ = w.Write(metadata)
		} else {
			_, _ = w.Write(archive)
		}
	}))
	t.Cleanup(server.Close)
	hooks := Hooks{Read: func(p string, max int64) ([]byte, error) { return os.ReadFile(p) }, Write: func(p string, b []byte) error { return os.WriteFile(p, b, 0600) }, Prepare: func(p string) error { return os.MkdirAll(p, 0700) }, Begin: func(force bool) (bool, error) { return !busy.Load() || force, nil }, End: func() {}, Install: func(plan Plan) error {
		b, e := os.ReadFile(filepath.Join(plan.Staged, "WebDeviceCable"))
		if e != nil {
			return e
		}
		if string(b) != "new version" {
			t.Error("wrong extracted update")
		}
		installed.Add(1)
		return nil
	}}
	m, e := New(Build{Version: "0.1.0", Official: true, PublicKey: base64.StdEncoding.EncodeToString(pub), OS: system, Arch: "amd64", Native: system == "darwin"}, filepath.Join(t.TempDir(), "updates"), hooks)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { m.cancel(); m.workers.Wait() })
	m.feed = server.URL + "/feed"
	m.client = &http.Client{Transport: transportFunc(func(req *http.Request) (*http.Response, error) {
		clone := req.Clone(req.Context())
		u, _ := url.Parse(server.URL)
		clone.URL = u
		clone.URL.Path = "/archive"
		if req.URL.Path == "/feed" {
			clone.URL.Path = "/feed"
		}
		return http.DefaultTransport.RoundTrip(clone)
	})}
	return m, busy, installed, &metadata
}
func checkManager(t *testing.T, m *Manager) {
	t.Helper()
	if e := m.Check(); e != nil {
		t.Fatal(e)
	}
	m.workers.Wait()
	if m.Status().Stage == "error" {
		t.Fatal(m.Status().Error)
	}
}
func TestAutomaticUpdateWaitsForWorkAndManualOverride(t *testing.T) {
	m, busy, installed, _ := managerFixture(t, "linux")
	if !m.Status().Automatic {
		t.Fatal("official auto default must be on")
	}
	checkManager(t, m)
	m.step()
	m.workers.Wait()
	if m.Status().Stage != "ready" {
		t.Fatal(m.Status())
	}
	busy.Store(true)
	m.step()
	if m.Status().Stage != "waiting" || installed.Load() != 0 {
		t.Fatal("interrupted active work")
	}
	if e := m.SetAutomatic(false); e != nil {
		t.Fatal(e)
	}
	busy.Store(false)
	m.step()
	if installed.Load() != 0 {
		t.Fatal("installed after disabling automatic updates")
	}
	busy.Store(true)
	if e := m.Install(true); e != nil {
		t.Fatal(e)
	}
	m.step()
	if installed.Load() != 1 {
		t.Fatal("explicit owner update did not install")
	}
}
func TestAutomaticIdleInstallAndPreferences(t *testing.T) {
	m, _, installed, _ := managerFixture(t, "linux")
	checkManager(t, m)
	m.step()
	m.workers.Wait()
	if e := m.SetAutomatic(false); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(filepath.Join(m.dir, "preferences.json"))
	var p preferences
	_ = json.Unmarshal(b, &p)
	if p.Automatic == nil || *p.Automatic || p.Highest != "0.2.0" {
		t.Fatal("did not persist preferences and replay protection")
	}
	if e := m.SetAutomatic(true); e != nil {
		t.Fatal(e)
	}
	m.step()
	if installed.Load() != 1 {
		t.Fatal("idle auto update did not install")
	}
	if m.SetAutomatic(false) == nil {
		t.Fatal("changed policy in the middle of a committed installation")
	}
}

func TestDownloadTamperingAndMetadataRollback(t *testing.T) {
	m, _, installed, metadata := managerFixture(t, "linux")
	checkManager(t, m)
	m.mu.Lock()
	m.plan.Artifact.SHA256 = strings.Repeat("0", 64)
	m.mu.Unlock()
	m.step()
	m.workers.Wait()
	if m.Status().Stage != "error" || installed.Load() != 0 {
		t.Fatal("accepted altered update")
	}
	m.prefs.Highest = "0.3.0"
	_ = metadata
	_ = m.Check()
	m.workers.Wait()
	if !strings.Contains(m.Status().Error, "older release") {
		t.Fatal("accepted rollback")
	}
}
func TestNativeUpdateUsesSameIdleBarrier(t *testing.T) {
	m, busy, _, _ := managerFixture(t, "darwin")
	checkManager(t, m)
	m.step()
	m.workers.Wait()
	if m.Status().Stage != "ready" {
		t.Fatal("native archive not verified before installation", m.Status())
	}
	if m.NativeReport("9.0.0", "ready", "") == nil {
		t.Fatal("accepted unrelated native update")
	}
	busy.Store(true)
	m.step()
	if m.NativePoll().Action == "install" {
		t.Fatal("native install interrupted work")
	}
	busy.Store(false)
	m.step()
	command := m.NativePoll()
	if command.Action != "install" || command.Manual || command.URL == "" || command.Size < 1 {
		t.Fatal("invalid native install authorization", command)
	}
	if e := m.NativeReport("0.2.0", "authorization", ""); e != nil {
		t.Fatal(e)
	}
	m.step()
	if m.NativePoll().Action != "" {
		t.Fatal("automatically retried an authorization prompt")
	}
	if e := m.Install(false); e != nil {
		t.Fatal(e)
	}
	m.step()
	if !m.NativePoll().Manual {
		t.Fatal("did not convey the owner's explicit installation choice")
	}
}

func TestDevelopmentBuildNeverChecksOrInstalls(t *testing.T) {
	m, _, _, _ := managerFixture(t, "linux")
	m.build.Official = false
	m.status.Supported = false
	m.status.Automatic = false
	m.status.Stage = "development"
	if m.Check() == nil || m.Install(false) == nil || m.SetAutomatic(true) == nil {
		t.Fatal("developer build accepted official update operation")
	}
}

func TestCommunityBuildKeepsManualUpdatesWithExistingAutomaticPreference(t *testing.T) {
	for _, system := range []string{"linux", "windows", "darwin"} {
		t.Run(system, func(t *testing.T) {
			base, _, installed, _ := managerFixture(t, system)
			dir := t.TempDir()
			if e := os.WriteFile(filepath.Join(dir, "preferences.json"), []byte(`{"automatic":true,"lastChecked":"0001-01-01T00:00:00Z"}`), 0600); e != nil {
				t.Fatal(e)
			}
			build := base.build
			build.Community = true // Even a conflicting official flag cannot enable updates.
			m, e := New(build, dir, base.hooks)
			if e != nil {
				t.Fatal(e)
			}
			m.client.Transport = transportFunc(func(*http.Request) (*http.Response, error) {
				t.Error("community build contacted the update service")
				return nil, errors.New("unexpected update request")
			})
			m.step()
			if status := m.Status(); status.Stage != "community" || !status.Community || status.Official || status.Automatic || status.Supported {
				t.Fatalf("incorrect community update status: %+v", status)
			}
			if m.Check() == nil || m.Install(true) == nil || m.SetAutomatic(true) == nil || installed.Load() != 0 {
				t.Fatal("community build accepted an upstream update")
			}
			m.cancel()
			m.workers.Wait()
		})
	}
}
func TestNoAutomaticIncompatibleMigration(t *testing.T) {
	m, _, installed, metadata := managerFixture(t, "linux")
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	m.build.PublicKey = base64.StdEncoding.EncodeToString(pub)
	r := sampleRelease([]byte("x"))
	r.StateVersion = 2
	*metadata = signedRelease(t, r, key)
	checkManager(t, m)
	m.step()
	if m.Status().Stage != "manual" || installed.Load() != 0 {
		t.Fatal("automatically migrated incompatible private state")
	}
}
func TestBoundedDownloadAndCancellation(t *testing.T) {
	m, _, installed, _ := managerFixture(t, "linux")
	checkManager(t, m)
	m.cancel()
	m.step()
	m.workers.Wait()
	if installed.Load() != 0 || m.Status().Stage != "error" {
		t.Fatal("cancelled download installed")
	}
}
func TestRootDirectoryAndDuplicateZip(t *testing.T) {
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	_, _ = z.Create("package/")
	a, _ := z.Create("package/file")
	_, _ = io.WriteString(a, "x")
	_ = z.Close()
	dir := t.TempDir()
	p := filepath.Join(dir, "update.zip")
	_ = os.WriteFile(p, b.Bytes(), 0600)
	if e := Extract(p, filepath.Join(dir, "out"), true); e != nil {
		t.Fatal(e)
	}
}

func TestUnreadableUpdateSettingsDoNotDisableOrdinaryApplication(t *testing.T) {
	m, _, _, _ := managerFixture(t, "linux")
	original := []byte("not valid json")
	_ = os.WriteFile(filepath.Join(m.dir, "preferences.json"), original, 0600)
	other, e := New(m.build, m.dir, m.hooks)
	if e != nil {
		t.Fatal("update settings failure should be isolated", e)
	}
	defer other.cancel()
	if other.Status().Supported || other.Status().Automatic || other.Status().Stage != "error" {
		t.Fatal("did not fail closed for updates")
	}
	after, _ := os.ReadFile(filepath.Join(m.dir, "preferences.json"))
	if !bytes.Equal(after, original) {
		t.Fatal("overwrote corrupt preferences")
	}
}
func TestUpdateCachePruningKeepsRecoveryAndDoesNotFollowLinks(t *testing.T) {
	m, _, _, _ := managerFixture(t, "linux")
	for _, name := range []string{"download-0.0.1", "download-0.0.2", "download-0.0.3", "install-100", "install-200", "install-300", "personal-backup"} {
		_ = os.Mkdir(filepath.Join(m.dir, name), 0700)
	}
	outside := filepath.Join(t.TempDir(), "keep")
	_ = os.Mkdir(outside, 0700)
	_ = os.WriteFile(filepath.Join(outside, "keep"), []byte("keep"), 0600)
	_ = os.Symlink(outside, filepath.Join(m.dir, "download-0.0.4"))
	m.prune(filepath.Join(m.dir, "download-0.0.1"))
	if _, e := os.Stat(filepath.Join(m.dir, "download-0.0.1")); e != nil {
		t.Fatal("removed current download")
	}
	if _, e := os.Stat(filepath.Join(m.dir, "personal-backup")); e != nil {
		t.Fatal("removed unknown directory")
	}
	if _, e := os.Stat(filepath.Join(outside, "keep")); e != nil {
		t.Fatal("followed directory link")
	}
	entries, _ := os.ReadDir(m.dir)
	downloads, installs := 0, 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if strings.HasPrefix(entry.Name(), "download-") {
			downloads++
		}
		if strings.HasPrefix(entry.Name(), "install-") {
			installs++
		}
	}
	if downloads > 2 || installs > 2 {
		t.Fatal("unbounded retained caches", downloads, installs)
	}
}
