package llmtypes

import (
	"slices"
	"testing"
)

func TestScopedEnvironmentUnsetsServerOwnedSecrets(t *testing.T) {
	ambient := []string{"PATH=/bin", "AUTH_SECRET=x", "ACCESS_PASSWORD=y", "AUTH_USERS=z", "GLOBAL_SECRET_STRIPE=w", "MY_TOOL=ok"}
	// no secret scope declared for the call: server-owned names are still removed, nothing else is
	_, unset := ScopedCodingAgentEnvironmentPlan(ambient, nil, &CallOptions{})
	for _, want := range []string{"AUTH_SECRET", "ACCESS_PASSWORD", "AUTH_USERS", "GLOBAL_SECRET_STRIPE"} {
		if !slices.Contains(unset, want) {
			t.Errorf("%s is not removed from the launch (unset=%v)", want, unset)
		}
	}
	for _, keep := range []string{"PATH", "MY_TOOL"} {
		if slices.Contains(unset, keep) {
			t.Errorf("%s was removed", keep)
		}
	}
}
