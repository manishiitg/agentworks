package orchestrator

import (
	"testing"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

// SetWorkspaceEnvRef replaces the env map wholesale. Secrets were already
// backfilled after that swap; variables were not, so a workflow that resolved
// its variables early lost every VAR_* as soon as any later initialization
// stored a fresh map. Observed on customer-login: 28 variables synced, absent
// from every env ref afterwards, and the agent re-derived SITE_URL from
// variables/variables.json by hand.
func TestWorkspaceVariablesSurviveEnvRefReplacement(t *testing.T) {
	bo := &BaseOrchestrator{logger: loggerv2.NewNoop()}
	bo.SetWorkspaceVariables(map[string]string{"SITE_URL": "https://staging.example", "LOGIN_EMAIL": "qa@example"})

	// A first env map, as initialization would build it.
	bo.SetWorkspaceEnvRef(map[string]string{"MCP_API_URL": "http://one"})
	if got := bo.GetWorkspaceEnvRef()["VAR_SITE_URL"]; got != "https://staging.example" {
		t.Fatalf("VAR_SITE_URL after first store = %q, want it present", got)
	}

	// A later store replaces the map entirely — this is the step that used to
	// drop the variables.
	bo.SetWorkspaceEnvRef(map[string]string{"MCP_API_URL": "http://two"})
	env := bo.GetWorkspaceEnvRef()
	if got := env["VAR_SITE_URL"]; got != "https://staging.example" {
		t.Fatalf("VAR_SITE_URL after replacement = %q, want it restored", got)
	}
	if got := env["VAR_LOGIN_EMAIL"]; got != "qa@example" {
		t.Fatalf("VAR_LOGIN_EMAIL after replacement = %q, want it restored", got)
	}
	if got := env["MCP_API_URL"]; got != "http://two" {
		t.Fatalf("MCP_API_URL = %q, want the new map's value", got)
	}
}

// Variables recorded before any env map exists must still land once one is set.
// This is the ordering that produced the original failure.
func TestWorkspaceVariablesRecordedBeforeAnyEnvMap(t *testing.T) {
	bo := &BaseOrchestrator{logger: loggerv2.NewNoop()}
	bo.SetWorkspaceVariables(map[string]string{"SITE_URL": "https://staging.example"})
	if bo.GetWorkspaceEnvRef() != nil {
		t.Fatal("precondition: no env map yet")
	}
	bo.SetWorkspaceEnvRef(map[string]string{})
	if got := bo.GetWorkspaceEnvRef()["VAR_SITE_URL"]; got != "https://staging.example" {
		t.Fatalf("VAR_SITE_URL = %q, want it applied to the first map that appears", got)
	}
}

// A later group-scoped load must add to what is known, not replace it.
func TestWorkspaceVariablesMergeRatherThanReplace(t *testing.T) {
	bo := &BaseOrchestrator{logger: loggerv2.NewNoop()}
	bo.SetWorkspaceVariables(map[string]string{"SITE_URL": "https://staging.example"})
	bo.SetWorkspaceVariables(map[string]string{"GROUP_NAME": "customer-staging"})
	bo.SetWorkspaceEnvRef(map[string]string{})
	env := bo.GetWorkspaceEnvRef()
	if env["VAR_SITE_URL"] == "" || env["VAR_GROUP_NAME"] == "" {
		t.Fatalf("second load dropped the first: %v", env)
	}
}

// Secrets must keep working exactly as before.
func TestSecretsStillBackfilledAlongsideVariables(t *testing.T) {
	bo := &BaseOrchestrator{logger: loggerv2.NewNoop(), secrets: []SecretEntry{{Name: "TOKEN", Value: "s3cr3t"}}}
	bo.SetWorkspaceVariables(map[string]string{"SITE_URL": "https://staging.example"})
	bo.SetWorkspaceEnvRef(map[string]string{})
	env := bo.GetWorkspaceEnvRef()
	if env["SECRET_TOKEN"] != "s3cr3t" || env["VAR_SITE_URL"] != "https://staging.example" {
		t.Fatalf("want both secret and variable backfilled, got %v", env)
	}
}

func TestReplaceWorkspaceVariablesRemovesRenamedAndDeletedValues(t *testing.T) {
	bo := &BaseOrchestrator{logger: loggerv2.NewNoop()}
	env := map[string]string{"MCP_API_URL": "http://bridge"}
	bo.SetWorkspaceEnvRef(env)
	bo.ReplaceWorkspaceVariables(map[string]string{"BASE_URL": "https://one", "OLD": "stale"})
	bo.ReplaceWorkspaceVariables(map[string]string{"BASE_URL": "https://two", "NEW": "fresh"})
	if _, exists := env["VAR_OLD"]; exists {
		t.Fatal("VAR_OLD remained after exact refresh")
	}
	if env["VAR_BASE_URL"] != "https://two" || env["VAR_NEW"] != "fresh" {
		t.Fatalf("unexpected refreshed env: %v", env)
	}
}
