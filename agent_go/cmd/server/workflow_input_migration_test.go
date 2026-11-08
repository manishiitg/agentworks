package server

import (
	"encoding/json"
	"strings"
	"testing"
)

// PLAT-746-450 / PR1537 review: validating only exposed names let broken raw
// mappings be written. Drive the actual migration command's shared validator.
func TestWorkflowInputMigrationRejectsInvalidRawMappingsBeforeApply(t *testing.T) {
	for _, tc := range []struct {
		name      string
		mapping   string
		fixed     string
		wantError bool
	}{
		{"valid", `{"source":"event.kind","values":{"issue":2},"default":1}`, `{}`, false},
		{"wrong typed choice", `{"source":"event.kind","values":{"issue":"invalid"}}`, `{}`, true},
		{"wrong typed default", `{"source":"event.kind","default":false}`, `{}`, true},
		{"missing path", `{"source":""}`, `{}`, true},
		{"malformed path", `{"source":"event..count"}`, `{}`, true},
		{"fixed input", `{"source":"event.count"}`, `{"count":"3"}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := `{"definitions":{"variables":[{"name":"count","type":"integer","required":true,"value":"1"}],"groups":[{"name":"Saved"}]},"functions":[{"name":"hook","action":{"type":"run"},"input":{"input_preset":"Saved","inputs":` + tc.fixed + `},"input_mapping":{"mode":"raw","variables":["count"],"payload":{"inputs":{"count":` + tc.mapping + `}}}}]}`
			if !json.Valid([]byte(candidate)) {
				t.Fatal("invalid fixture JSON")
			}
			// Omitted mode is raw too; the migration must not skip its
			// actual mapping validation just because the default was saved.
			for _, body := range []string{candidate, strings.Replace(candidate, `"mode":"raw",`, "", 1)} {
				err := validateWorkflowInputMigration(strings.NewReader(body))
				if (err != nil) != tc.wantError {
					t.Fatalf("unexpected validation: %v", err)
				}
				if err != nil && !strings.Contains(err.Error(), "count") {
					t.Fatalf("refusal must identify the input: %v", err)
				}
			}
		})
	}
	// PR1544 review4506503823: raw selected names without mappings must
	// still be declared, while a valid unmapped selection may use a default.
	for _, names := range []string{`["count"]`, `["count","missing"]`} {
		candidate := `{"definitions":{"variables":[{"name":"count","type":"integer","value":"1"}]},"functions":[{"name":"hook","action":{"type":"run"},"input_mapping":{"mode":"raw","variables":` + names + `}}]}`
		err := validateWorkflowInputMigration(strings.NewReader(candidate))
		invalid := strings.Contains(names, "missing")
		if (err != nil) != invalid {
			t.Fatalf("selected raw names: %v", err)
		}
		if invalid && !strings.Contains(err.Error(), "missing") {
			t.Fatalf("refusal must name the invalid selection: %v", err)
		}
	}

}
