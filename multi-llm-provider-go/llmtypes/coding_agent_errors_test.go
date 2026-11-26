package llmtypes

import (
	"fmt"
	"strings"
	"testing"
)

func TestCodingAgentAuthRequiredError(t *testing.T) {
	err := fmt.Errorf("startup failed: %w", &CodingAgentAuthRequiredError{
		Provider:     "cursor-cli",
		LoginCommand: "cursor-agent login",
		Detail:       "login screen detected",
	})

	if !IsCodingAgentAuthRequiredError(err) {
		t.Fatalf("IsCodingAgentAuthRequiredError(%v) = false, want true", err)
	}
	for _, want := range []string{"cursor-cli authentication required", "cursor-agent login", "login screen detected"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not contain %q", err, want)
		}
	}
}
