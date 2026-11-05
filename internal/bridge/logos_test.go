package bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConnectionReviewPermissionsLogoAndPersistence(t *testing.T) {
	f := setup(t)
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><path fill="#123456" d="M0 0h24v24z"/></svg>`)
	logoID, err := f.a.storeLogo(svg)
	if err != nil {
		t.Fatal(err)
	}
	for _, automatic := range []bool{true, false} {
		id := secret()
		input := map[string]any{"id": id, "appId": "review", "name": "Review app", "appUser": "alice", "origin": "https://review.example", "logoURL": "https://review.example/logo.svg", "requestedPermissions": map[string]bool{"automaticApprovals": true}}
		object(t, f.req("POST", "/api/pairing-requests", f.origin, "", "", input, true), 201)
		pending := object(t, f.req("GET", "/api/state", f.origin, "", "", nil, true), 200)["pairingRequests"].([]any)
		if pending[0].(map[string]any)["logoURL"] != input["logoURL"] || len(f.a.state.Invitations) != 0 {
			t.Fatal("review lost suggestions or granted access early")
		}
		input["requestedPermissions"] = map[string]bool{"automaticApprovals": false}
		object(t, f.req("POST", "/api/pairing-requests", f.origin, "", "", input, true), 409)
		path := "/api/pairing-requests/" + id
		object(t, f.req("POST", path+"/decision", f.origin, "", "", map[string]any{"approve": true, "automatic": automatic, "logoId": logoID}, true), 200)
		request := object(t, f.req("GET", path, f.origin, "", "", nil, true), 200)
		claim := object(t, f.req("POST", "/v2/pairings/claim", "https://review.example", request["invitation"].(string), "", map[string]string{"appId": "review", "appUser": "alice"}, false), 201)
		pair := claim["pairing"].(map[string]any)
		if pair["automatic"] != automatic || pair["logoId"] != logoID || pair["mode"] != "restricted" {
			t.Fatal(pair)
		}
		pairID := pair["id"].(string)
		object(t, f.req("PATCH", "/api/pairings/"+pairID, f.origin, "", "", map[string]string{"logoId": secret()}, true), 400)
		object(t, f.req("PATCH", "/api/pairings/"+pairID, f.origin, "", "", map[string]string{"logoId": ""}, true), 200)
		if f.a.state.Pairings[pairID].LogoID != "" {
			t.Fatal("removal did not persist")
		}
		object(t, f.req("PATCH", "/api/pairings/"+pairID, f.origin, "", "", map[string]string{"logoId": logoID}, true), 200)
		// Verify the durable state, not only the mutation response.
		raw, err := os.ReadFile(filepath.Join(f.a.dir, "state.json"))
		if err != nil {
			t.Fatal(err)
		}
		var persisted State
		if json.Unmarshal(raw, &persisted) != nil || persisted.Pairings[pairID].LogoID != logoID || persisted.Pairings[pairID].Automatic != automatic {
			t.Fatal("review choices were not saved")
		}
	}
	object(t, f.req("GET", "/api/logos/"+logoID, f.origin, "", "", nil, false), 401)
	response := f.req("GET", "/api/logos/"+logoID, f.origin, "", "", nil, true)
	if response.Code != 200 || response.Header().Get("Content-Type") != "image/svg+xml" || !bytes.Equal(response.Body.Bytes(), svg) {
		t.Fatal("cached logo changed")
	}
}

func TestLogoValidationAndFetchBoundaries(t *testing.T) {
	for _, bad := range []string{
		`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`,
		`<svg onload="alert(1)"/>`, `<svg><image href="http://127.0.0.1"/></svg>`,
		`<!DOCTYPE svg [<!ENTITY x SYSTEM "file:///etc/passwd">]><svg>&x;</svg>`,
		`<svg><path fill="url(https://example.com/tracker)"/></svg>`, `<svg/><svg/>`,
		`<html/>`, strings.Repeat("x", logoLimit+1),
	} {
		if _, err := logoType([]byte(bad)); err == nil {
			t.Fatalf("unsafe image accepted: %.80s", bad)
		}
	}
	var raster bytes.Buffer
	_ = png.Encode(&raster, image.NewRGBA(image.Rect(0, 0, 32, 32)))
	if media, err := logoType(raster.Bytes()); err != nil || media != "image/png" {
		t.Fatal(media, err)
	}
	if _, err := logoType(raster.Bytes()[:len(raster.Bytes())-8]); err == nil {
		t.Fatal("truncated PNG accepted")
	}
	var large bytes.Buffer
	_ = png.Encode(&large, image.NewRGBA(image.Rect(0, 0, 2049, 1)))
	if _, err := logoType(large.Bytes()); err == nil {
		t.Fatal("oversized dimensions accepted")
	}
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "/image", 302)
			return
		}
		if r.URL.Path == "/big" {
			w.Write([]byte(strings.Repeat("x", logoLimit+1)))
			return
		}
		w.Write(raster.Bytes())
	}))
	defer source.Close()
	data, err := fetchLogo(context.Background(), source.URL+"/image", source.URL)
	if err != nil || !bytes.Equal(data, raster.Bytes()) {
		t.Fatal("same-origin dev logo failed", err)
	}
	for _, url := range []string{source.URL + "/redirect", "http://169.254.169.254/latest/meta-data/", "file:///etc/passwd", "https://127.0.0.1/logo"} {
		if _, err := fetchLogo(context.Background(), url, "https://review.example"); err == nil {
			t.Fatal("unsafe URL accepted", url)
		}
	}
	data, err = fetchLogo(context.Background(), source.URL+"/big", source.URL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := logoType(data); err == nil {
		t.Fatal("unbounded fetched image accepted")
	}
	f := setup(t)
	upload := func(data []byte, authorized bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", f.origin+"/api/logos/upload", bytes.NewReader(data))
		req.RemoteAddr = "127.0.0.1:12345"
		req.Header.Set("Origin", f.origin)
		if authorized {
			req.Header.Set("X-Bridge-Session", f.sessionID)
			req.Header.Set("X-Bridge-CSRF", f.csrf)
		}
		w := httptest.NewRecorder()
		f.handler.ServeHTTP(w, req)
		return w
	}
	object(t, upload(raster.Bytes(), false), 401)
	result := object(t, upload(raster.Bytes(), true), 201)
	if result["logoId"] == "" {
		t.Fatal(result)
	}
	object(t, upload([]byte(strings.Repeat("x", logoLimit+1)), true), 413)
	object(t, upload([]byte(`<svg onload="x"/>`), true), 400)
	object(t, f.req("POST", "/api/logos", f.origin, "", "", map[string]string{"url": source.URL + "/image", "origin": source.URL}, true), 201)
}
