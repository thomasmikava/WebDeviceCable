package bridge

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strconv"
)

func ValidateRuntime(info RuntimeInfo) error {
	u, err := url.Parse(info.URL)
	if err != nil {
		return err
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port < 1 || port > 65535 || info.PID < 1 || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Path != "" || u.RawPath != "" || u.Host != fmt.Sprintf("127.0.0.1:%d", port) {
		return errors.New("invalid runtime address")
	}
	return nil
}

// The launcher proves the peer owns the local control key without sending it.
func RuntimeProof(key, challenge string, info RuntimeInfo) string {
	mac := hmac.New(sha256.New, []byte(key))
	fmt.Fprintf(mac, "webdevicecable/runtime/v1\n%s\n%s\n%d", challenge, info.URL, info.PID)
	return hex.EncodeToString(mac.Sum(nil))
}
