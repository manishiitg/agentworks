package agycli

import (
	"regexp"
	"strings"
)

var agyDiagnosticBase64 = regexp.MustCompile(`[A-Za-z0-9+/=]{64,}`)
var agyDiagnosticAssignment = regexp.MustCompile(`(?i)(?:authorization|api[_-]?key|token|secret|password)\s*[:=]\s*[^,;\s]+`)

// The existing tail retains at most 1 KiB. Sanitize BEFORE the 200-character
// cut: truncating a long payload first could disguise it as a short token.
func agyACPStderrLine(tail string, environment []string) string {
	last := ""
	for _, line := range strings.Split(tail, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			last = line
		}
	}
	for _, entry := range environment {
		name, value, _ := strings.Cut(entry, "=")
		name = strings.ToUpper(name)
		if value != "" && (strings.Contains(name, "KEY") || strings.Contains(name, "TOKEN") || strings.Contains(name, "SECRET") || strings.Contains(name, "PASSWORD")) {
			last = strings.ReplaceAll(last, value, "[omitted]")
		}
	}
	last = agyDiagnosticAssignment.ReplaceAllString(last, "[omitted]")
	last = agyDiagnosticBase64.ReplaceAllString(last, "[omitted]")
	runes := []rune(strings.ToValidUTF8(last, ""))
	if len(runes) > 200 {
		runes = runes[:200]
	}
	return string(runes)
}

func (r *agyACP) stderrLine() string {
	if r.stderr == nil {
		return ""
	}
	var environment []string
	if r.cmd != nil {
		environment = r.cmd.Env
	}
	return agyACPStderrLine(r.stderr.String(), environment)
}
