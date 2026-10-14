package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// The importer's parser must understand real .env files and must never put a value into what the CLI prints.
func TestParseDotEnvEdgeCasesAndNoValueInReports(t *testing.T) {
	file := parseDotEnv("\ufeff# comment\n" +
		"PLAIN=abc123\n" +
		"export EXPORTED = spaced-value # inline comment\n" +
		"DOUBLE=\"line1\\nline2 with \\\"quotes\\\" and # hash\"\n" +
		"SINGLE='literal \\n # not a comment'\n" +
		"MULTI=\"first\nsecond\"\n" +
		"EMPTY=\n" +
		"1BAD=hunter2-bad-name\n" +
		"no equals sign here hunter2-no-equals\n" +
		"DUP=one\nDUP=two\n" +
		"UNCLOSED=\"hunter2-unclosed\n")
	want := map[string]string{
		"PLAIN":    "abc123",
		"EXPORTED": "spaced-value",
		"DOUBLE":   "line1\nline2 with \"quotes\" and # hash",
		"SINGLE":   `literal \n # not a comment`,
		"MULTI":    "first\nsecond",
		"DUP":      "two",
	}
	for name, value := range want {
		if file.Values[name] != value {
			t.Errorf("%s = %q, want %q", name, file.Values[name], value)
		}
	}
	if _, ok := file.Values["UNCLOSED"]; ok || len(file.Values) != len(want) {
		t.Errorf("unexpected names parsed: %v", file.Order)
	}
	if got := strings.Join(file.Order, ","); got != "PLAIN,EXPORTED,DOUBLE,SINGLE,MULTI,DUP" {
		t.Errorf("order = %s", got)
	}
	// Reports name lines and reasons, never any part of a value.
	report := strings.Join(file.Invalid, "\n")
	for _, secret := range []string{"hunter2", "abc123", "spaced-value", "literal"} {
		if strings.Contains(report, secret) {
			t.Errorf("a value reached the skipped-line report: %q", report)
		}
	}
	if len(file.Invalid) != 4 {
		t.Errorf("skipped lines = %v, want empty value, bad name, no equals and unclosed quote", file.Invalid)
	}
	// What the CLI prints from the server's answer carries names and outcomes only.
	raw, _ := json.Marshal(map[string]any{"results": []map[string]string{{"name": "PLAIN", "status": "added"}}})
	var out strings.Builder
	o := &options{stdout: &out, stderr: &out}
	if err := printSecretResults(o, raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "abc123") {
		t.Errorf("output contains a value: %q", out.String())
	}
}
