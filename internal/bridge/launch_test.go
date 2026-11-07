package bridge

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

func TestLaunchURIRootPathNormalization(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	id := strings.Repeat("a", 64)
	for _, tc := range []struct {
		host   string
		fields url.Values
	}{
		{"connect", url.Values{"requestId": {id}, "origin": {"https://website.example"}, "publicKey": {publicKey(key)}}},
		{"relay-connect", url.Values{"relayUrl": {"https://relay.example"}, "code": {id}}},
		{"settings", url.Values{"relayUrl": {"https://relay.example"}, "connectionId": {id}}},
	} {
		t.Run(tc.host, func(t *testing.T) {
			base := "webdevicecable://" + tc.host
			query := "?" + tc.fields.Encode()
			want, err := ParseLaunchURI(base + query)
			if err != nil {
				t.Fatal(err)
			}
			got, err := ParseLaunchURI(base + "/" + query)
			if err != nil {
				t.Fatalf("rejected Windows-normalized link: %v", err)
			}
			// Relay intent IDs hash the original URL; all other fields agree.
			if tc.host != "connect" {
				got.ID = want.ID
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("normalization changed intent: got %+v, want %+v", got, want)
			}
			for _, path := range []string{"/path", "//", "/./", "/../", "/%2f", "%2f", "/%2e"} {
				if _, err := ParseLaunchURI(base + path + query); err == nil {
					t.Errorf("accepted path %q", path)
				}
			}
			for _, suffix := range []string{"#fragment", "&extra=1", "&" + tc.fields.Encode()} {
				if _, err := ParseLaunchURI(base + "/" + query + suffix); err == nil {
					t.Errorf("accepted invalid suffix %q", suffix)
				}
			}
		})
	}
}
