package redaction

import "testing"

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
