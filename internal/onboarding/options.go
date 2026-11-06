// Package onboarding describes suggestions shown during local connection review.
// Suggestions never grant access until the desktop owner approves them.
package onboarding

import (
	"errors"
	"net"
	"net/url"
)

type Permissions struct {
	AutomaticApprovals bool `json:"automaticApprovals,omitempty"`
	Files              bool `json:"files,omitempty"`
}
type Options struct {
	LogoURL              string      `json:"logoURL,omitempty"`
	RequestedPermissions Permissions `json:"requestedPermissions,omitzero"`
}

func (o Options) Validate(origin string) error {
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
