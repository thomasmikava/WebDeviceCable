package bridge

import (
	"regexp"
	"strings"
	"unicode"
)

type Failure struct {
	Code      string `json:"code"`
	Category  string `json:"category"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
	Raw       string `json:"raw,omitempty"`
}

func failureFor(status int, code, message string) *Failure {
	category := "internal"
	retry := false
	switch {
	case code == "bridge_updating":
		category = "unavailable"
		retry = true
	case code == "verification_failed":
		category = "security"
	case code == "tab_disconnected" || code == "lease_expired" || code == "lease_closed":
		category = "disconnected"
	case strings.Contains(code, "deadline") || status == 408:
		category = "timeout"
	case strings.Contains(code, "context_limit"):
		category = "context_limit"
	case status == 429 || code == "rate_limited":
		category = "rate_limit"
		retry = true
	case status == 401 || code == "authentication_required" || code == "api_key_not_allowed":
		category = "authentication"
	case status == 403 || code == "permission_denied" || code == "native_account_changed" || strings.Contains(code, "consent") || strings.Contains(code, "access") || strings.Contains(code, "revoked"):
		category = "permission"
	case strings.Contains(code, "unverified") || strings.Contains(code, "unresolved") || strings.HasSuffix(code, "_unavailable") || code == "execution_unavailable" || status == 503:
		category = "unavailable"
	case code == "session_protocol_error" || code == "native_protocol_error":
		category = "protocol"
	case code == "command_failed" || code == "command_start_failed" || code == "native_execution_failed" || code == "invalid_model_output" || code == "image_generation_failed":
		category = "execution"
	case code == "route_changed" || code == "groups_changed":
		category = "conflict"
	case strings.Contains(code, "cancel") || strings.Contains(code, "declined") || strings.Contains(code, "paused"):
		category = "cancelled"
	case strings.Contains(code, "restarted") || strings.Contains(code, "stopped") || strings.Contains(code, "session_closed") || strings.Contains(code, "session_expired"):
		category = "interrupted"
	case status == 409 || code == "session_busy":
		category = "conflict"
	case status == 400 || status == 404 || status == 415:
		category = "validation"
	case status == 410:
		category = "interrupted"
	}
	return &Failure{Code: code, Category: category, Message: message, Retryable: retry}
}

var secretDiagnostic = regexp.MustCompile(`(?i)\bbearer\s+[^\s,;"'}]+|\b(?:api[_-]?key|authorization|(?:access|refresh)[_-]?token|password|client[_-]?secret)\b["']?\s*[:=]\s*["']?(?:bearer\s+)?[^\s,;"'}]+|\bsk-[A-Za-z0-9_-]{8,}`)

func cleanDiagnostic(raw string) string {
	if len(raw) > 8192 {
		raw = raw[:8192]
	}
	raw = strings.ToValidUTF8(raw, "�")
	raw = strings.Map(func(c rune) rune {
		if unicode.IsControl(c) && c != '\n' && c != '\t' {
			return -1
		}
		return c
	}, raw)
	raw = secretDiagnostic.ReplaceAllString(raw, "[redacted]")
	if len(raw) > 4096 {
		raw = strings.ToValidUTF8(raw[:4096], "")
	}
	return raw
}
