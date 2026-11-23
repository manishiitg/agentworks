package llmtypes

import (
	"strings"
	"testing"
)

func scopedEnvOpts(environment map[string]string) *CallOptions {
	opts := &CallOptions{}
	WithCodingAgentSecretEnvironment(environment)(opts)
	return opts
}

func envMap(entries []string) map[string]string {
	out := make(map[string]string, len(entries))
	for _, entry := range entries {
		if key, value, found := strings.Cut(entry, "="); found {
			out[key] = value
		}
	}
	return out
}

// The isolation guarantee is about the environment a child process actually
// receives. Overlaying declared values on os.Environ() without removing
// UNDECLARED scoped credentials left that guarantee untrue: a child could read
// whatever secrets happened to be in the launcher's environment.
func TestMergeDropsUndeclaredAmbientCredentials(t *testing.T) {
	ambient := []string{
		"PATH=/usr/bin",
		"SECRET_OTHER_TENANT=leaked-value",
		"VAR_SOMEONE_ELSES_CONFIG=leaked-config",
		"SECRET_MINE=stale-ambient",
	}
	opts := scopedEnvOpts(map[string]string{"SECRET_MINE": "declared-value"})

	got := envMap(MergeCodingAgentSecretEnvironment(ambient, opts))

	if _, leaked := got["SECRET_OTHER_TENANT"]; leaked {
		t.Fatalf("undeclared ambient secret reached the child: %v", got)
	}
	if _, leaked := got["VAR_SOMEONE_ELSES_CONFIG"]; leaked {
		t.Fatalf("undeclared ambient VAR_ reached the child: %v", got)
	}
	if got["SECRET_MINE"] != "declared-value" {
		t.Fatalf("declared value did not win over the ambient one: %q", got["SECRET_MINE"])
	}
	if got["PATH"] != "/usr/bin" {
		t.Fatalf("an unrelated ambient variable was scrubbed: %v", got)
	}
}

// Address-style MCP routes are still inherited: over-filtering them previously
// produced a silent failure where the child never saw MCP_CUSTOM and nothing
// errored, and an address grants nothing on its own.
func TestMergeKeepsAmbientMCPAddressRoutes(t *testing.T) {
	ambient := []string{
		"MCP_API_URL=http://bridge.local",
		"MCP_CUSTOM=http://bridge.local/custom",
		"MCP_MCP=http://bridge.local/mcp",
		"MCP_VIRTUAL=http://bridge.local/virtual",
	}
	opts := scopedEnvOpts(map[string]string{"SECRET_MINE": "v"})

	got := envMap(MergeCodingAgentSecretEnvironment(ambient, opts))

	for _, key := range []string{"MCP_API_URL", "MCP_CUSTOM", "MCP_MCP", "MCP_VIRTUAL"} {
		if got[key] == "" {
			t.Fatalf("address route %s was scrubbed, which silently breaks the bridge: %v", key, got)
		}
	}
}

// The credential half of the MCP_* prefix is NOT routing metadata: an ambient
// bearer token or session id lets a child act with another session's authority
// and binding. Treating the whole prefix as inheritable was the gap.
func TestMergeDropsAmbientMCPCredentialsAndSessionIdentity(t *testing.T) {
	ambient := []string{
		"MCP_API_URL=http://bridge.local",
		"MCP_API_TOKEN=another-sessions-token",
		"MCP_AUTH=Bearer another-sessions-token",
		"MCP_SESSION_ID=another-session",
	}
	opts := scopedEnvOpts(map[string]string{"SECRET_MINE": "v"})

	got := envMap(MergeCodingAgentSecretEnvironment(ambient, opts))

	for _, key := range []string{"MCP_API_TOKEN", "MCP_AUTH", "MCP_SESSION_ID"} {
		if _, leaked := got[key]; leaked {
			t.Fatalf("child inherited another session's %s: %v", key, got)
		}
	}
	if got["MCP_API_URL"] != "http://bridge.local" {
		t.Fatalf("scrubbing credentials must not take the address route with it: %v", got)
	}
}

// A caller that DOES declare its own session credentials must get exactly
// those, not the ambient ones.
func TestMergeDeclaredMCPCredentialsWinOverAmbient(t *testing.T) {
	ambient := []string{"MCP_API_TOKEN=another-sessions-token", "MCP_SESSION_ID=another-session"}
	opts := scopedEnvOpts(map[string]string{"MCP_API_TOKEN": "my-token", "MCP_SESSION_ID": "my-session"})

	got := envMap(MergeCodingAgentSecretEnvironment(ambient, opts))

	if got["MCP_API_TOKEN"] != "my-token" || got["MCP_SESSION_ID"] != "my-session" {
		t.Fatalf("declared session credentials did not win: %v", got)
	}
}

// With nothing declared the caller has opted out of scoping entirely, so the
// environment must pass through untouched -- scrubbing here would break every
// existing caller that never used this feature.
func TestMergeWithoutADeclaredScopeIsPassthrough(t *testing.T) {
	ambient := []string{"PATH=/usr/bin", "SECRET_AMBIENT=kept"}
	got := envMap(MergeCodingAgentSecretEnvironment(ambient, &CallOptions{}))
	if got["SECRET_AMBIENT"] != "kept" || got["PATH"] != "/usr/bin" {
		t.Fatalf("passthrough changed the environment: %v", got)
	}
}

// A tmux pane inherits the tmux SERVER's environment, so there is no slice to
// filter the way a structured launch filters cmd.Env. The equivalent has to be
// expressed as export/unset and executed inside the launch script -- these pin
// that plan.
func TestScopedPlanExportsDeclaredAndUnsetsUndeclared(t *testing.T) {
	ambient := []string{
		"PATH=/usr/bin",
		"SECRET_OTHER_TENANT=leaked",
		"VAR_NOT_MINE=leaked",
		"MCP_API_TOKEN=another-sessions-token",
		"MCP_API_URL=http://bridge.local",
	}
	adapterOwned := []string{"MCP_API_TOKEN=my-own-derived-token"}
	opts := scopedEnvOpts(map[string]string{"SECRET_MINE": "v"})

	export, unset := ScopedCodingAgentEnvironmentPlan(ambient, adapterOwned, opts)

	if len(export) != 1 || export[0] != "SECRET_MINE=v" {
		t.Fatalf("export = %v, want the declared entry", export)
	}
	unsetSet := map[string]bool{}
	for _, key := range unset {
		unsetSet[key] = true
	}
	for _, want := range []string{"SECRET_OTHER_TENANT", "VAR_NOT_MINE"} {
		if !unsetSet[want] {
			t.Fatalf("undeclared %s was not scheduled for unset: %v", want, unset)
		}
	}
	// The adapter derived this one itself from the caller's config. Unsetting
	// it would strip the credential the launch just built.
	if unsetSet["MCP_API_TOKEN"] {
		t.Fatalf("adapter-owned credential was scheduled for unset: %v", unset)
	}
	// An address grants nothing and dropping it silently breaks the bridge.
	if unsetSet["MCP_API_URL"] {
		t.Fatalf("address route was scheduled for unset: %v", unset)
	}
	if unsetSet["PATH"] {
		t.Fatalf("unrelated variable was scheduled for unset: %v", unset)
	}
}

func TestScopedEnvironmentCarriesClaudeAutoMemoryControl(t *testing.T) {
	const key = "CLAUDE_CODE_DISABLE_AUTO_MEMORY"
	opts := scopedEnvOpts(map[string]string{key: "1"})

	merged := envMap(MergeCodingAgentSecretEnvironment(nil, opts))
	if merged[key] != "1" {
		t.Fatalf("structured environment omitted %s: %v", key, merged)
	}
	export, _ := ScopedCodingAgentEnvironmentPlan(nil, nil, opts)
	if len(export) != 1 || export[0] != key+"=1" {
		t.Fatalf("interactive environment omitted %s: %v", key, export)
	}
}

// Without a declared scope the caller never opted in, so an interactive launch
// must be byte-identical to what it was before -- no exports, no unsets.
func TestScopedPlanIsInertWithoutADeclaredScope(t *testing.T) {
	export, unset := ScopedCodingAgentEnvironmentPlan(
		[]string{"SECRET_AMBIENT=kept", "PATH=/usr/bin"},
		nil,
		&CallOptions{},
	)
	if len(export) != 0 || len(unset) != 0 {
		t.Fatalf("plan must be inert without a declared scope: export=%v unset=%v", export, unset)
	}
}

// "Grant this child nothing" is a real policy and used to be unexpressible:
// an explicitly empty map wrote no metadata, so it was indistinguishable from
// never calling the option, and both fell through to passthrough -- the child
// kept every ambient credential, the exact opposite of the request.
func TestExplicitlyEmptyScopeScrubsEverything(t *testing.T) {
	ambient := []string{
		"PATH=/usr/bin",
		"SECRET_ANY=leaked",
		"VAR_ANY=leaked",
		"MCP_API_TOKEN=another-sessions-token",
	}
	opts := scopedEnvOpts(map[string]string{})

	if !CodingAgentScopeDeclared(opts) {
		t.Fatal("an explicitly empty scope must still count as declared")
	}

	got := envMap(MergeCodingAgentSecretEnvironment(ambient, opts))
	for _, key := range []string{"SECRET_ANY", "VAR_ANY", "MCP_API_TOKEN"} {
		if _, leaked := got[key]; leaked {
			t.Fatalf("empty scope still let %s through: %v", key, got)
		}
	}
	if got["PATH"] != "/usr/bin" {
		t.Fatalf("empty scope must not touch unrelated variables: %v", got)
	}

	_, unset := ScopedCodingAgentEnvironmentPlan(ambient, nil, opts)
	unsetSet := map[string]bool{}
	for _, key := range unset {
		unsetSet[key] = true
	}
	for _, key := range []string{"SECRET_ANY", "VAR_ANY", "MCP_API_TOKEN"} {
		if !unsetSet[key] {
			t.Fatalf("empty scope did not schedule %s for unset in the tmux plan: %v", key, unset)
		}
	}
}

// The counterpart that must keep working: a caller that never supplied the
// option at all is legacy passthrough, not "scrub everything".
func TestNoOptionAtAllRemainsPassthrough(t *testing.T) {
	opts := &CallOptions{}
	if CodingAgentScopeDeclared(opts) {
		t.Fatal("no option must not count as a declared scope")
	}
	got := envMap(MergeCodingAgentSecretEnvironment([]string{"SECRET_AMBIENT=kept"}, opts))
	if got["SECRET_AMBIENT"] != "kept" {
		t.Fatalf("callers that never opted in must be unaffected: %v", got)
	}
}

// The Go filter and the shell-matching policy are the same rule expressed
// twice; if they drift, one boundary silently stops scrubbing something the
// other still considers a credential.
func TestScrubPolicyDataMatchesTheGoFilter(t *testing.T) {
	for _, prefix := range ScopedCredentialPrefixes() {
		if !isScopedCredentialEnvironmentKey(prefix + "ANYTHING") {
			t.Fatalf("prefix %q is exported for shell scrubbing but the Go filter ignores it", prefix)
		}
	}
	for _, name := range ScopedCredentialNames() {
		if !isScopedCredentialEnvironmentKey(name) {
			t.Fatalf("name %q is exported for shell scrubbing but the Go filter ignores it", name)
		}
	}
	// Addresses must stay out of both.
	for _, addr := range []string{"MCP_API_URL", "MCP_CUSTOM", "MCP_MCP", "MCP_VIRTUAL"} {
		if isScopedCredentialEnvironmentKey(addr) {
			t.Fatalf("%s is an address, not a credential", addr)
		}
		for _, name := range ScopedCredentialNames() {
			if name == addr {
				t.Fatalf("%s must not be in the shell scrub list", addr)
			}
		}
	}
}

// A retained CLI applies its environment once, at launch. The fingerprint is
// what lets an adapter notice that a live process is running under a stale
// scope -- without it, turn 2 declaring a narrower scope silently kept turn
// 1's credentials alive and revocation required killing the session by hand.
func TestScopeFingerprintChangesWhenTheScopeChanges(t *testing.T) {
	base := CodingAgentScopeFingerprint(scopedEnvOpts(map[string]string{"SECRET_A": "1"}))

	if same := CodingAgentScopeFingerprint(scopedEnvOpts(map[string]string{"SECRET_A": "1"})); same != base {
		t.Fatal("an unchanged scope must reuse the session, not churn it")
	}
	// A changed VALUE is as much a revocation as a changed key.
	if rotated := CodingAgentScopeFingerprint(scopedEnvOpts(map[string]string{"SECRET_A": "2"})); rotated == base {
		t.Fatal("rotating a credential value must change the fingerprint")
	}
	if added := CodingAgentScopeFingerprint(scopedEnvOpts(map[string]string{"SECRET_A": "1", "SECRET_B": "1"})); added == base {
		t.Fatal("granting an additional credential must change the fingerprint")
	}
	// Removal is the case that matters most: narrowing must not be silent.
	if removed := CodingAgentScopeFingerprint(scopedEnvOpts(map[string]string{})); removed == base {
		t.Fatal("removing every credential must change the fingerprint")
	}

	// The three "nothing granted" states are all distinct.
	unscoped := CodingAgentScopeFingerprint(&CallOptions{})
	empty := CodingAgentScopeFingerprint(scopedEnvOpts(map[string]string{}))
	if unscoped == empty {
		t.Fatal("no scope and an explicitly empty scope must be distinguishable")
	}
	if unscoped == base || empty == base {
		t.Fatal("an unscoped session must not look like a scoped one")
	}
}

// Key/value boundaries must not collide: ("AB","C") and ("A","BC") are
// different grants and have to fingerprint differently.
func TestScopeFingerprintHasNoBoundaryCollisions(t *testing.T) {
	a := CodingAgentScopeFingerprint(scopedEnvOpts(map[string]string{"SECRET_AB": "C"}))
	b := CodingAgentScopeFingerprint(scopedEnvOpts(map[string]string{"SECRET_A": "BC"}))
	if a == b {
		t.Fatal("length-prefixing is missing: distinct scopes collided")
	}
}

// The fingerprint is stored on session state; it must never carry the
// credential itself.
func TestScopeFingerprintDoesNotLeakCredentialValues(t *testing.T) {
	const secret = "super-secret-token-value"
	got := CodingAgentScopeFingerprint(scopedEnvOpts(map[string]string{"SECRET_MINE": secret}))
	if strings.Contains(got, secret) || strings.Contains(got, "SECRET_MINE") {
		t.Fatalf("fingerprint leaks scope contents: %q", got)
	}
}

// The host's Supabase admin key (used to email invitations) is in the
// server's own environment, so a scoped agent that shares that environment
// must have it removed, like any other credential it did not declare.
func TestScopedPlanUnsetsTheHostsSupabaseAdminKey(t *testing.T) {
	ambient := []string{"PATH=/usr/bin", "SUPABASE_SERVICE_ROLE_KEY=admin-key", "SUPABASE_URL=https://x.supabase.co"}
	_, unset := ScopedCodingAgentEnvironmentPlan(ambient, nil, scopedEnvOpts(map[string]string{"SECRET_MINE": "v"}))
	unsetSet := map[string]bool{}
	for _, key := range unset {
		unsetSet[key] = true
	}
	if !unsetSet["SUPABASE_SERVICE_ROLE_KEY"] {
		t.Fatalf("the Supabase admin key reaches a scoped agent: %v", unset)
	}
	if unsetSet["SUPABASE_URL"] || unsetSet["PATH"] {
		t.Fatalf("an address or unrelated variable was scrubbed: %v", unset)
	}
}
