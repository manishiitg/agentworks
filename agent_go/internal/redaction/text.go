// Package redaction shares the persisted chat/terminal secret policy.
package redaction

import (
	"regexp"
	"strings"
)

const sensitiveAssignment = `(?i)\b((?:[A-Z0-9_]*(?:API[_-]?KEY|TOKEN|SECRET|PASSWORD|PASSWD)|SECRET_[A-Z0-9_]+)\s*[=:]\s*)`

var (
	// These are necessary substrings of the existing policies, not new secret
	// rules. Native journal exports redact every string, most of which cannot
	// contain an assignment; avoid three broad identifier scans for those.
	assignmentMarker = regexp.MustCompile(`(?i)(?:API[_-]?KEY|TOKEN|SECRET|PASSWORD|PASSWD|SECRET_[A-Z0-9_]+)\s*[=:]`)
	bearerMarker     = regexp.MustCompile(`(?i)Bearer\s`)
	quotedDouble     = regexp.MustCompile(sensitiveAssignment + `"(?:\\.|[^"\\\r\n])*"`)
	quotedSingle     = regexp.MustCompile(sensitiveAssignment + `'(?:\\.|[^'\\\r\n])*'`)
	mcpToken         = regexp.MustCompile(`(?i)(MCP_API_TOKEN=)[^\s"'\\]+`)
	sensitiveEnv     = regexp.MustCompile(`(?i)\b([A-Z0-9_]*(?:API[_-]?KEY|TOKEN|SECRET|PASSWORD|PASSWD)\s*[=:]\s*)[^\s"'\\]+`)
	bearerToken      = regexp.MustCompile(`(?i)(\bBearer\s+)[^\s"'\\]+`)
	secretEnv        = regexp.MustCompile(`(?m)(SECRET_[A-Z0-9_]+=)[^\s"'\\]+`)
	providerSKKey    = regexp.MustCompile(`\bsk-[A-Za-z0-9][A-Za-z0-9_-]{10,}\b`)
	googleAPIKey     = regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{20,}\b`)
	jsonSecret       = regexp.MustCompile(`(?i)("(?:[a-z0-9_-]*(?:api[_-]?key|token|secret|password|passwd)|authorization|cookie|set-cookie)"\s*:\s*)"(?:\\.|[^"\\])*"`)
)

// Text removes common credential forms, including quoted JSON tool fields,
// without parsing or changing ordinary prose, ANSI sequences or tool structure.
func Text(value string) string {
	value = jsonSecret.ReplaceAllString(value, `${1}"[redacted]"`)
	hasAssignment := assignmentMarker.MatchString(value)
	if hasAssignment {
		value = quotedDouble.ReplaceAllString(value, `${1}"[redacted]"`)
		value = quotedSingle.ReplaceAllString(value, `${1}'[redacted]'`)
		value = mcpToken.ReplaceAllString(value, "$1[redacted]")
		value = sensitiveEnv.ReplaceAllString(value, "$1[redacted]")
	}
	if bearerMarker.MatchString(value) {
		value = bearerToken.ReplaceAllString(value, "$1[redacted]")
	}
	if hasAssignment {
		value = secretEnv.ReplaceAllString(value, "$1[redacted]")
	}
	if strings.Contains(value, "sk-") {
		value = providerSKKey.ReplaceAllString(value, "sk-[redacted]")
	}
	if strings.Contains(value, "AIza") {
		value = googleAPIKey.ReplaceAllString(value, "AIza[redacted]")
	}
	return value
}
