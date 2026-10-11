// Package redaction shares the persisted chat/terminal secret policy.
package redaction

import "regexp"

var (
	mcpToken      = regexp.MustCompile(`(?i)(MCP_API_TOKEN=)[^\s"'\\]+`)
	sensitiveEnv  = regexp.MustCompile(`(?i)\b([A-Z0-9_]*(?:API[_-]?KEY|TOKEN|SECRET|PASSWORD|PASSWD)\s*[=:]\s*)[^\s"'\\]+`)
	bearerToken   = regexp.MustCompile(`(?i)(\bBearer\s+)[^\s"'\\]+`)
	secretEnv     = regexp.MustCompile(`(?m)(SECRET_[A-Z0-9_]+=)[^\s"'\\]+`)
	providerSKKey = regexp.MustCompile(`\bsk-[A-Za-z0-9][A-Za-z0-9_-]{10,}\b`)
	googleAPIKey  = regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{20,}\b`)
	jsonSecret    = regexp.MustCompile(`(?i)("(?:[a-z0-9_-]*(?:api[_-]?key|token|secret|password|passwd)|authorization|cookie|set-cookie)"\s*:\s*)"(?:\\.|[^"\\])*"`)
)

// Text removes common credential forms, including quoted JSON tool fields,
// without parsing or changing ordinary prose, ANSI sequences or tool structure.
func Text(value string) string {
	value = jsonSecret.ReplaceAllString(value, `${1}"[redacted]"`)
	value = mcpToken.ReplaceAllString(value, "$1[redacted]")
	value = sensitiveEnv.ReplaceAllString(value, "$1[redacted]")
	value = bearerToken.ReplaceAllString(value, "$1[redacted]")
	value = secretEnv.ReplaceAllString(value, "$1[redacted]")
	value = providerSKKey.ReplaceAllString(value, "sk-[redacted]")
	return googleAPIKey.ReplaceAllString(value, "AIza[redacted]")
}
