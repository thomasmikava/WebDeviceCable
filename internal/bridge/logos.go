package bridge

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/thomasmikava/WebDeviceCable/internal/onboarding"
)

const logoLimit = 256 << 10

// Resolve and dial the same public address. Redirects and proxies cannot bypass
// this check. Development logos may use only the requesting app's loopback origin.
func fetchLogo(ctx context.Context, raw, origin string) ([]byte, error) {
	if err := (onboarding.Options{LogoURL: raw}).Validate(origin); err != nil {
		return nil, err
	}
	u, _ := url.Parse(raw)
	app, _ := url.Parse(origin)
	allowLoopback := app != nil && u.Scheme == app.Scheme && u.Host == app.Host
	transport := &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		for _, entry := range ips {
			ip := entry.IP
			if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
				if !(allowLoopback && ip.IsLoopback()) {
					return nil, errors.New("logo address must be public, or the app's loopback address")
				}
			}
		}
		if len(ips) == 0 {
			return nil, errors.New("logo host has no address")
		}
		return (&net.Dialer{}).DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
		return errors.New("logo redirects are not supported; use the final image URL")
	}}
	req, err := http.NewRequestWithContext(ctx, "GET", raw, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "image/png,image/jpeg,image/gif,image/svg+xml")
	response, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return nil, errors.New("logo URL did not return an image")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, logoLimit+1))
	if err != nil {
		return nil, err
	}
	return data, nil
}

func logoType(data []byte) (string, error) {
	invalid := errors.New("Choose a PNG, JPEG, GIF or static SVG, up to 256 KiB and 2048 × 2048 pixels.")
	if len(data) == 0 || len(data) > logoLimit {
		return "", invalid
	}
	if config, format, err := image.DecodeConfig(bytes.NewReader(data)); err == nil {
		if config.Width < 1 || config.Height < 1 || config.Width > 2048 || config.Height > 2048 {
			return "", invalid
		}
		// Validate the entire raster, including truncated files.
		if _, _, err = image.Decode(bytes.NewReader(data)); err != nil {
			return "", invalid
		}
		return map[string]string{"png": "image/png", "jpeg": "image/jpeg", "gif": "image/gif"}[format], nil
	}
	// Deliberately small SVG subset: no scripts, CSS, references or embedded content.
	decoder := xml.NewDecoder(bytes.NewReader(data))
	depth := 0
	root := false
	tags := "|svg|g|path|rect|circle|ellipse|line|polyline|polygon|title|desc|defs|linearGradient|radialGradient|stop|clipPath|"
	attrs := "|xmlns|version|viewBox|width|height|x|y|x1|x2|y1|y2|cx|cy|r|rx|ry|d|points|fill|fill-rule|fill-opacity|stroke|stroke-width|stroke-linecap|stroke-linejoin|stroke-opacity|opacity|transform|id|offset|stop-color|stop-opacity|gradientUnits|gradientTransform|clip-path|"
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", invalid
		}
		switch value := token.(type) {
		case xml.StartElement:
			if depth == 0 {
				if root || value.Name.Local != "svg" {
					return "", invalid
				}
				root = true
			}
			depth++
			if depth > 32 || value.Name.Space != "" && value.Name.Space != "http://www.w3.org/2000/svg" || !strings.Contains(tags, "|"+value.Name.Local+"|") {
				return "", invalid
			}
			for _, attr := range value.Attr {
				v := strings.ToLower(attr.Value)
				if !strings.Contains(attrs, "|"+attr.Name.Local+"|") || strings.Contains(v, "url(") || strings.Contains(v, ":") && attr.Name.Local != "xmlns" || attr.Name.Space != "" && attr.Name.Local != "xmlns" {
					return "", invalid
				}
			}
		case xml.EndElement:
			depth--
		case xml.Directive:
			return "", invalid
		case xml.ProcInst:
			if root || value.Target != "xml" {
				return "", invalid
			}
		case xml.CharData:
			if depth == 0 && strings.TrimSpace(string(value)) != "" {
				return "", invalid
			}
		}
	}
	if !root || depth != 0 {
		return "", invalid
	}
	return "image/svg+xml", nil
}
func (a *App) logoPath(id string) string { return filepath.Join(a.dir, "logos", id) }
func (a *App) checkLogo(id string) error {
	if id == "" {
		return nil
	}
	if !validStorageID(id) {
		return fail(400, "invalid_logo", "Choose a logo first.")
	}
	if info, err := os.Lstat(a.logoPath(id)); err != nil || !info.Mode().IsRegular() {
		return fail(400, "invalid_logo", "Choose a logo first.")
	}
	return nil
}
func (a *App) storeLogo(data []byte) (string, error) {
	if _, err := logoType(data); err != nil {
		return "", fail(400, "invalid_logo", err.Error())
	}
	id := digest(string(data))
	if a.checkLogo(id) == nil {
		return id, nil
	}
	dir := filepath.Join(a.dir, "logos")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	referenced := map[string]bool{}
	for _, p := range a.state.Pairings {
		referenced[p.LogoID] = true
	}
	for _, i := range a.state.Invitations {
		referenced[i.Pairing.LogoID] = true
	}
	var size int64
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return "", err
		}
		if !referenced[entry.Name()] && time.Since(info.ModTime()) > 24*time.Hour {
			if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil {
				return "", err
			}
			continue
		}
		size += info.Size()
	}
	if size+int64(len(data)) > 32<<20 {
		return "", fail(413, "logo_storage_full", "Logo cache is full. Try again after unused previews expire.")
	}
	if err := writeLocalFile(a.logoPath(id), data); err != nil {
		return "", err
	}
	return id, nil
}
func (a *App) logoRoutes(mux *http.ServeMux, control func(func(http.ResponseWriter, *http.Request) error) http.HandlerFunc) {
	mux.HandleFunc("POST /api/logos", control(func(w http.ResponseWriter, r *http.Request) error {
		var in struct {
			URL    string `json:"url"`
			Origin string `json:"origin"`
		}
		if err := decode(w, r, &in); err != nil {
			return err
		}
		// Authenticate under the control lock, but never hold it during network I/O.
		var data []byte
		var err error
		func() { a.mu.Unlock(); defer a.mu.Lock(); data, err = fetchLogo(r.Context(), in.URL, in.Origin) }()
		if err != nil {
			return fail(400, "logo_unavailable", "Could not fetch the logo. Use a direct image URL or upload an image.")
		}
		id, err := a.storeLogo(data)
		if err != nil {
			return err
		}
		writeJSON(w, 201, map[string]string{"logoId": id})
		return nil
	}))
	mux.HandleFunc("POST /api/logos/upload", control(func(w http.ResponseWriter, r *http.Request) error {
		data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, logoLimit))
		if err != nil {
			return fail(413, "logo_too_large", "Logo must be at most 256 KiB.")
		}
		id, err := a.storeLogo(data)
		if err != nil {
			return err
		}
		writeJSON(w, 201, map[string]string{"logoId": id})
		return nil
	}))
	mux.HandleFunc("GET /api/logos/{id}", control(func(w http.ResponseWriter, r *http.Request) error {
		id := r.PathValue("id")
		if err := a.checkLogo(id); err != nil || id == "" {
			return fail(404, "not_found", "Logo not found.")
		}
		data, err := os.ReadFile(a.logoPath(id))
		if err != nil {
			return err
		}
		media, err := logoType(data)
		if err != nil {
			return err
		}
		w.Header().Set("Content-Type", media)
		w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Write(data)
		return nil
	}))
}
