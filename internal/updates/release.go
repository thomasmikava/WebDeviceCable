// Package updates verifies maintainer-authorized releases before any installation.
package updates

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

const MaxDownload int64 = 256 << 20
const MaxExpanded int64 = 768 << 20
const Repository = "thomasmikava/WebDeviceCable"
const Feed = "https://github.com/" + Repository + "/releases/latest/download/updates.json"
const Appcast = "https://github.com/" + Repository + "/releases/latest/download/appcast.xml"

// Build values are compiled into official builds; they are never accepted from websites.
type Build struct {
	Version   string `json:"version"`
	Official  bool   `json:"official"`
	Community bool   `json:"community,omitempty"`
	PublicKey string `json:"-"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`
	Native    bool   `json:"-"`
}
type Artifact struct {
	OS     string `json:"os"`
	Arch   string `json:"arch"`
	URL    string `json:"url"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}
type Release struct {
	Format          int        `json:"format"`
	Version         string     `json:"version"`
	StateVersion    int        `json:"stateVersion"`
	ProtocolVersion int        `json:"protocolVersion"`
	Artifacts       []Artifact `json:"artifacts"`
}

// Sign the exact payload bytes, prefixed with a domain separator.
type Envelope struct {
	Payload   string `json:"payload"`
	Signature string `json:"signature"`
}

var versionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
var hashPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

func Version(v string) ([3]uint64, error) {
	var result [3]uint64
	if !versionPattern.MatchString(v) {
		return result, errors.New("a stable semantic version is required")
	}
	for i, p := range strings.Split(v, ".") {
		n, e := strconv.ParseUint(p, 10, 32)
		if e != nil {
			return result, e
		}
		result[i] = n
	}
	return result, nil
}
func Newer(a, b string) bool {
	x, e := Version(a)
	y, f := Version(b)
	if e != nil || f != nil {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return x[i] > y[i]
		}
	}
	return false
}
func strict(data []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		return e
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("one JSON value required")
	}
	return nil
}
func Message(payload []byte) []byte {
	return append([]byte("WebDeviceCable release v1\x00"), payload...)
}
func Verify(raw []byte, key string) (Release, error) {
	var r Release
	var env Envelope
	if len(raw) > 128<<10 || strict(raw, &env) != nil {
		return r, errors.New("invalid release metadata")
	}
	pub, e := base64.StdEncoding.DecodeString(key)
	if e != nil || len(pub) != ed25519.PublicKeySize {
		return r, errors.New("no trusted release key configured")
	}
	data, e := base64.StdEncoding.DecodeString(env.Payload)
	if e != nil {
		return r, errors.New("invalid release payload")
	}
	sig, e := base64.StdEncoding.DecodeString(env.Signature)
	if e != nil || !ed25519.Verify(pub, Message(data), sig) {
		return r, errors.New("release signature verification failed")
	}
	if strict(data, &r) != nil || r.Format != 1 || r.StateVersion < 1 || r.ProtocolVersion < 1 || len(r.Artifacts) < 1 || len(r.Artifacts) > 12 {
		return r, errors.New("unsupported release metadata")
	}
	if _, e = Version(r.Version); e != nil {
		return r, e
	}
	seen := map[string]bool{}
	for _, a := range r.Artifacts {
		id := a.OS + "/" + a.Arch
		if seen[id] || !validTarget(a.OS, a.Arch) || a.Size < 1 || a.Size > MaxDownload || !hashPattern.MatchString(a.SHA256) || !validAsset(a.URL, r.Version) {
			return r, errors.New("invalid release artifact")
		}
		seen[id] = true
	}
	return r, nil
}
func validTarget(system, arch string) bool {
	return (system == "darwin" || system == "windows" || system == "linux") && (arch == "amd64" || arch == "arm64")
}
func validAsset(raw, version string) bool {
	u, e := url.Parse(raw)
	return e == nil && u.Scheme == "https" && u.Host == "github.com" && u.User == nil && u.RawQuery == "" && u.Fragment == "" && u.RawPath == "" && strings.HasPrefix(u.Path, "/"+Repository+"/releases/download/v"+version+"/") && !strings.ContainsAny(strings.TrimPrefix(u.Path, "/"+Repository+"/releases/download/v"+version+"/"), "/\\%")
}
func (r Release) Target(b Build) (Artifact, error) {
	for _, a := range r.Artifacts {
		if a.OS == b.OS && a.Arch == b.Arch {
			return a, nil
		}
	}
	return Artifact{}, fmt.Errorf("no update for %s/%s", b.OS, b.Arch)
}
func Hash(data []byte) string { v := sha256.Sum256(data); return hex.EncodeToString(v[:]) }
