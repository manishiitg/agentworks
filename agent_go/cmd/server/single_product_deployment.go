package server

import (
	"os"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productpolicy"
)

// productEnabled reports whether a product's profiles and skills should be
// loaded by this server. An unset AGENT_PRODUCTS preserves the shared-server
// behavior of loading every product. Dedicated deployments can set a
// comma-separated allowlist (for example, "example-app") so unrelated
// product startup failures cannot take down their agent API.
func allowlistHasProduct(configured, product string) bool {
	for _, candidate := range strings.Split(configured, ",") {
		if strings.EqualFold(strings.TrimSpace(candidate), product) {
			return true
		}
	}
	return false
}

func productEnabled(product string) bool { return productpolicy.Enabled(product) }

// isSingleProductServerDeployment reports whether this server instance is
// dedicated to exactly one product surface
// via AGENT_PRODUCTS, as opposed to the shared desktop/multi-product server
// where AGENT_PRODUCTS is unset. Only a genuinely dedicated deployment is
// eligible for the missing-Claude-Code-token refusal in handleQuery: on the
// desktop app, falling back to the user's own locally logged-in `claude` CLI
// is correct, intended behavior, not a bug to guard against.
func isSingleProductServerDeployment() bool {
	if localProductInstallation() {
		return false
	}
	configured := strings.TrimSpace(os.Getenv("AGENT_PRODUCTS"))
	if configured == "" {
		return false
	}
	count := 0
	for _, candidate := range strings.Split(configured, ",") {
		if strings.TrimSpace(candidate) != "" {
			count++
		}
	}
	return count == 1
}

// claudeCodeTokenMissingForSingleProductDeployment reports whether handleQuery
// requires explicit authentication: an active product profile resolves to the
// claude-code provider with no configured token, and either (a) this server
// is a dedicated single-product deployment (see
// isSingleProductServerDeployment -- the default safety net every product
// gets for free on such a deployment, no manifest change needed), or (b) the
// profile itself declares Runtime.RequireProviderToken (product.yaml opt-in
// for a profile that must never rely on an ambient CLI login regardless of
// deployment context). The query preflight may also accept an admitted
// account's own isolated login; this predicate never inspects ambient CLI auth.
func claudeCodeTokenMissingForSingleProductDeployment(resolvedProfile *resolvedAgentProfile, finalProvider string) bool {
	if resolvedProfile == nil || !strings.EqualFold(finalProvider, "claude-code") {
		return false
	}
	// Code uses the server's shared Claude login the way Crew does (owner, 2026-10-16: "ok fix it", after Code
	// refused a message that Crew accepted); only a single-product deployment or a profile that opts in refuses.
	if !isSingleProductServerDeployment() && !resolvedProfile.Definition.Runtime.RequireProviderToken {
		return false
	}
	if resolvedProfile.APIKeys == nil || resolvedProfile.APIKeys.ClaudeCodeOAuthToken == nil {
		return true
	}
	return strings.TrimSpace(*resolvedProfile.APIKeys.ClaudeCodeOAuthToken) == ""
}
