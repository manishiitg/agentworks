package store

import (
	"strings"
	"unicode"
)

// A connection's label is shown on every screen, tool reply and log that lists connections, so it must never hold a
// credential (PLAT-856-400: a pasted Notion token became a connection's name). A label that looks like one is
// replaced with the provider name.

var credentialPrefixes = []string{"ntn_", "secret_", "xoxb-", "xoxp-", "xoxa-", "xoxe-", "xapp-", "ghp_", "gho_", "ghu_", "ghs_", "github_pat_", "glpat-", "sk-", "sk_live_", "sk_test_", "rk_live_", "AKIA", "ASIA", "AIza", "ya29.", "Bearer "}

// LooksLikeCredential reports whether s reads as an API key or token rather than a name.
func LooksLikeCredential(s string) bool {
	s = strings.TrimSpace(s)
	for _, prefix := range credentialPrefixes {
		if strings.HasPrefix(s, prefix) && len(s) > len(prefix)+8 {
			return true
		}
	}
	if len(s) < 32 || strings.ContainsAny(s, " \t") {
		return false
	}
	var upper, lower, digit bool
	for _, r := range s {
		switch {
		case unicode.IsUpper(r):
			upper = true
		case unicode.IsLower(r):
			lower = true
		case unicode.IsDigit(r):
			digit = true
		}
	}
	return upper && lower && digit
}

func safeConnectorLabel(c Connector) Connector {
	if LooksLikeCredential(c.Label) {
		c.Label = c.Provider
	}
	return c
}

// relabelCredentialLabelsLocked repairs stored connections whose label looks like a credential and returns how many.
func (s *MemoryStore) relabelCredentialLabelsLocked(workspace string) int {
	fixed := 0
	for id, c := range s.connectors {
		if c.WorkspaceID == workspace && LooksLikeCredential(c.Label) {
			s.connectors[id] = safeConnectorLabel(c)
			fixed++
		}
	}
	return fixed
}
