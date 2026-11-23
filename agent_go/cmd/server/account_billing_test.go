package server

import "testing"

// #3433: a company Claude account on an Anthropic API key was shown and costed
// as a subscription. Only a setup-token (sk-ant-oat…) is a subscription.
func TestClaudeTokenBillingTellsAPIKeyFromSubscription(t *testing.T) {
	for token, want := range map[string]string{
		"sk-ant-oat01-example": billingSubscription,
		"sk-ant-api03-example": billingAPIKey,
		"sk-ant-usr-example":   billingAPIKey,
		"":                     "",
	} {
		if got := claudeTokenBilling(token); got != want {
			t.Errorf("claudeTokenBilling(%q) = %q, want %q", token, got, want)
		}
	}
}
