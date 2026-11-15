// Package onboarding describes suggestions shown during local connection review.
// Suggestions never grant access until the desktop owner approves them.
package onboarding

import (
	"errors"
	"net"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"
)

type Permissions struct {
	AutomaticApprovals bool `json:"automaticApprovals,omitempty"`
	Files              bool `json:"files,omitempty"`
}
type Options struct {
	LogoURL              string      `json:"logoURL,omitempty"`
	RequestedPermissions Permissions `json:"requestedPermissions,omitzero"`
	// UserLabel is how the app names the signed-in user, such as a name or email.
	// It is shown to the desktop owner only and never used for authorization.
	UserLabel string `json:"userLabel,omitempty"`
}

var ErrUserLabel = errors.New("userLabel must be 1 to 200 characters of visible text, without line breaks or control characters")

// ValidUserLabel accepts an empty label (none was sent) or one line of visible text.
func ValidUserLabel(label string) bool {
	if label == "" {
		return true
	}
	if !utf8.ValidString(label) || utf8.RuneCountInString(label) > 200 || strings.TrimSpace(label) == "" {
		return false
	}
	for _, r := range label {
		// Bidirectional overrides could reorder the website name shown beside the label.
		if unicode.IsControl(r) || r >= 0x202a && r <= 0x202e || r >= 0x2066 && r <= 0x2069 || r == 0x2028 || r == 0x2029 {
			return false
		}
	}
	return true
}

func (o Options) Validate(origin string) error {
	if !ValidUserLabel(o.UserLabel) {
		return ErrUserLabel
	}
	if o.LogoURL == "" {
		return nil
	}
	u, err := url.Parse(o.LogoURL)
	if err != nil || len(o.LogoURL) > 2048 || u.Host == "" || u.User != nil || u.Fragment != "" {
		return errors.New("use a logo HTTPS URL, or the app's loopback HTTP URL")
	}
	if u.Scheme == "https" {
		return nil
	}
	app, err := url.Parse(origin)
	ip := net.ParseIP(u.Hostname())
	if err == nil && u.Scheme == "http" && (u.Hostname() == "localhost" || ip != nil && ip.IsLoopback()) && u.Scheme == app.Scheme && u.Host == app.Host {
		return nil
	}
	return errors.New("use a logo HTTPS URL, or the app's loopback HTTP URL")
}
