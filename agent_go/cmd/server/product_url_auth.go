package server

import (
	"net/url"
	"strings"
	"unicode"
)

// OAuth destinations stay on this app, held server-side with the one-use state.
// Validate before redirect normalization, including nested encoded separators.
func safeProductAuthReturn(value string) string {
	if value == "" || len(value) > 8192 {
		return ""
	}
	path := strings.SplitN(value, "?", 2)[0]
	path = strings.SplitN(path, "#", 2)[0]
	for i := 0; i < 6; i++ {
		if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || strings.Contains(path, "\\") || strings.ContainsFunc(path, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) }) {
			return ""
		}
		for _, part := range strings.Split(path, "/") {
			if part == "." || part == ".." {
				return ""
			}
		}
		next, err := url.PathUnescape(path)
		if err != nil {
			return ""
		}
		if next == path {
			u, err := url.Parse(value)
			if err != nil || u.IsAbs() || u.Host != "" {
				return ""
			}
			root := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")[0]
			switch root {
			case "", "goals", "agentworks", "workflows", "relays", "crew", "work", "brain", "knowledgebase", "vault", "mcp-gateway", "llm-gateway", "bullpen", "agentbridge":
				u.Fragment = ""
				return u.String()
			}
			return ""
		}
		path = next
	}
	return ""
}
