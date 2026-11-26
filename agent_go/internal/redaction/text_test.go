package redaction

import (
	"strings"
	"testing"
)

func TestQuotedScriptCredentials(t *testing.T) {
	for _, fixture := range []struct{ input, want string }{
		{"MCP_API_TOKEN = 'fixture-secret'\nprint('ordinary code')", "MCP_API_TOKEN = '[redacted]'\nprint('ordinary code')"},
		{`export SECRET_SERVICE="fixture secret"`, `export SECRET_SERVICE="[redacted]"`},
		{`API_KEY="escaped\"secret"`, `API_KEY="[redacted]"`},
	} {
		if got := Text(fixture.input); got != fixture.want {
			t.Fatalf("quoted credential redaction changed source structure: got %q, want %q", got, fixture.want)
		}
	}
}

// #2989: offline journal exports run this policy for every string. The cheap
// gates must not omit any match of the original credential policy, including
// Go regexp's Unicode simple-fold matches and whitespace across lines.
func TestTextPrefilterKeepsCredentialPolicy(t *testing.T) {
	parts := []string{
		"ordinary prose: tokens and passwords are names, not assignments",
		"API_KEY=fixture-key", "api-key:fixture-key", "xTOKEN = fixture-token",
		"SECRET_SERVICE=fixture-secret", "SECRET_SERVICE: 'quoted fixture'",
		"PASSWORD\n : \tfixture-password", "PASSWD='fixture password'",
		`MCP_API_TOKEN="escaped\"fixture"`, "mcp_api_token=fixture-token",
		`{"authorization":"fixture auth","set-cookie":"fixture cookie"}`,
		`{"api_key":"fixture-key","secret":"fixture-secret","token":"fixture-token"}`,
		"Bearer\tfixture-bearer", "bearer\nfixture-bearer",
		"sk-fixture1234567890", "AIza0123456789abcdefghijk",
		"API_KEY=fixture-key", "ſECRET_SERVICE='fixture secret'",
		"PAſſWORD=fixture-password", "ſecret_name:fixture-secret",
		"\x1b[32mTOKEN=fixture-token\x1b[0m", "", "not-a-token",
	}
	for _, left := range parts {
		for _, right := range parts {
			input := left + "\n" + right
			if got, want := Text(input), textWithoutPrefilter(input); got != want {
				t.Fatalf("prefilter changed credential policy for %q: got %q, want %q", input, got, want)
			}
		}
	}
}

// Freeze the old replacement pipeline as a policy-parity and benchmark oracle;
// the production patterns and replacement order have not changed.
func textWithoutPrefilter(value string) string {
	value = jsonSecret.ReplaceAllString(value, `${1}"[redacted]"`)
	value = quotedDouble.ReplaceAllString(value, `${1}"[redacted]"`)
	value = quotedSingle.ReplaceAllString(value, `${1}'[redacted]'`)
	value = mcpToken.ReplaceAllString(value, "$1[redacted]")
	value = sensitiveEnv.ReplaceAllString(value, "$1[redacted]")
	value = bearerToken.ReplaceAllString(value, "$1[redacted]")
	value = secretEnv.ReplaceAllString(value, "$1[redacted]")
	value = providerSKKey.ReplaceAllString(value, "sk-[redacted]")
	return googleAPIKey.ReplaceAllString(value, "AIza[redacted]")
}

func BenchmarkTextOfflineJournal(b *testing.B) {
	prose := strings.Repeat("Read the project files and report the result. A token name is not a credential. ", 100)
	for _, fixture := range []struct{ name, text string }{
		{"ordinary", prose},
		{"credential", prose + ` API_KEY="fixture secret" Bearer fixture-bearer`},
	} {
		for _, impl := range []struct {
			name   string
			redact func(string) string
		}{
			{"before", textWithoutPrefilter}, {"prefilter", Text},
		} {
			b.Run(fixture.name+"/"+impl.name, func(b *testing.B) {
				b.SetBytes(int64(len(fixture.text)))
				b.ReportAllocs()
				for b.Loop() {
					impl.redact(fixture.text)
				}
			})
		}
	}
}
