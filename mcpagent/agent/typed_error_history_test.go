package mcpagent

import (
	"fmt"
	"github.com/manishiitg/multi-llm-provider-go/llmerrors"
	"testing"
)

func TestTypedCauseOverridesTerminalHistory(t *testing.T) {
	for _, tt := range []struct {
		kind llmerrors.Kind
		want string
	}{
		{llmerrors.KindTimeout, "connection_error"},
		{llmerrors.KindUserInputRequired, "user_input_required"},
		{llmerrors.KindCanceled, ""},
		{llmerrors.KindAuth, "auth_error"},
		{llmerrors.KindRateLimit, "throttling_error"},
	} {
		err := fmt.Errorf("adapter: %w", &llmerrors.Error{Kind: tt.kind, Err: fmt.Errorf("pane history: 429 quota exhausted; invalid api key; context length exceeded")})
		if got := classifyLLMError(err); got != tt.want {
			t.Errorf("%s: got %q want %q", tt.kind, got, tt.want)
		}
	}
}
