package terminals

import (
	"strings"
	"testing"
)

func TestRedactSensitiveTerminalTextCoversProviderKeys(t *testing.T) {
	googleKey := strings.Join([]string{"AIza", "Syabcdefghijklmnopqrstuvwxyz1234567"}, "")
	input := strings.Join([]string{
		"OPENAI_API_KEY=sk-proj-abcdefghijklmnopqrstuvwxyz123456",
		"ANTHROPIC_API_KEY=sk-ant-api03-abcdefghijklmnopqrstuvwxyz123456",
		"raw key sk-or-v1-abcdefghijklmnopqrstuvwxyz123456",
		"GOOGLE_API_KEY=" + googleKey,
		"raw google " + googleKey,
		"Authorization: Bearer sk-proj-abcdefghijklmnopqrstuvwxyz123456",
	}, "\n")

	got := RedactSensitiveTerminalText(input)
	for _, leaked := range []string{
		"sk-proj-abcdefghijklmnopqrstuvwxyz123456",
		"sk-ant-api03-abcdefghijklmnopqrstuvwxyz123456",
		"sk-or-v1-abcdefghijklmnopqrstuvwxyz123456",
		googleKey,
	} {
		if strings.Contains(got, leaked) {
			t.Fatalf("redacted text leaked %q:\n%s", leaked, got)
		}
	}
	for _, want := range []string{
		"OPENAI_API_KEY=[redacted]",
		"ANTHROPIC_API_KEY=[redacted]",
		"sk-[redacted]",
		"GOOGLE_API_KEY=[redacted]",
		"AIza[redacted]",
		"Authorization: Bearer [redacted]",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("redacted text missing %q:\n%s", want, got)
		}
	}
}
