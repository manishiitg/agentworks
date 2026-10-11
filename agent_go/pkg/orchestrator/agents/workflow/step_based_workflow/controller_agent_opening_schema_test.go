package step_based_workflow

import (
	"strings"
	"testing"
)

func TestAgentValidationSchemaJSONNilReturnsEmpty(t *testing.T) {
	if got := agentSequenceValidationSchemaJSON(nil); got != "" {
		t.Fatalf("agentSequenceValidationSchemaJSON(nil) = %q; want empty", got)
	}
}

func TestAgentValidationSchemaJSONRendersRequiredFiles(t *testing.T) {
	schema := &ValidationSchema{
		Files: []FileValidationRule{{
			FileName:  "results.json",
			MustExist: true,
			JSONChecks: []JSONValidationCheck{{
				Path:      "$.status",
				MustExist: true,
			}},
		}},
	}

	got := agentSequenceValidationSchemaJSON(schema)
	if !strings.Contains(got, "results.json") || !strings.Contains(got, "$.status") {
		t.Fatalf("schema JSON missing content: %q", got)
	}
}
