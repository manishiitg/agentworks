package server

import (
	"strings"
	"testing"
)

func profileWithPlacement(placement map[string][]string) *resolvedAgentProfile {
	profile := &resolvedAgentProfile{}
	profile.Definition.Runtime.Workspace.Placement = placement
	return profile
}

// A product declares where its own artifacts go; it must not inherit
// AgentWorks' layout (plan folders, pulse/goals.html) for concepts it does not
// have. Example Product's system prompt names uploads//work//outputs/, and the
// tool description is what the model re-reads on every get_api_spec call, so
// the two contradicting each other is the bug this guards.
func TestMultiAgentToolDescriptionUsesDeclaredPlacement(t *testing.T) {
	const folder = "_users/default/Chats/Example Product/projects/demo"
	profile := profileWithPlacement(map[string][]string{
		"execute_shell_command": {"Direct-chat work belongs in `work/`; finished exports belong in `outputs/`."},
	})

	shell := enhanceToolDescriptionForMultiAgentMode("execute_shell_command", "base.", folder, profile)
	if !strings.Contains(shell, "finished exports belong in `outputs/`") {
		t.Fatalf("declared placement missing from shell description:\n%s", shell)
	}
	for _, leaked := range []string{"pulse/", "goals.html", "org-pulse.html", "{plan_id}"} {
		if strings.Contains(shell, leaked) {
			t.Fatalf("product description leaked AgentWorks guidance %q:\n%s", leaked, shell)
		}
	}
	// The write-scope restriction is true everywhere and must survive.
	if !strings.Contains(shell, folder) || !strings.Contains(shell, "read-only unless explicitly allowed") {
		t.Fatalf("product description dropped the write-scope restriction:\n%s", shell)
	}

}

// Declaring nothing for a tool yields no placement lines. Falling back to the
// AgentWorks layout here would reintroduce exactly what this replaced.
func TestMultiAgentToolDescriptionUndeclaredToolGetsNoPlacement(t *testing.T) {
	const folder = "_users/default/Chats/Example Product/projects/demo"
	got := enhanceToolDescriptionForMultiAgentMode("execute_shell_command", "base.", folder, profileWithPlacement(nil))
	for _, leaked := range []string{"pulse/", "{plan_id}"} {
		if strings.Contains(got, leaked) {
			t.Fatalf("undeclared tool fell back to AgentWorks guidance %q:\n%s", leaked, got)
		}
	}
	if !strings.Contains(got, "read-only unless explicitly allowed") {
		t.Fatalf("undeclared tool lost the write-scope restriction:\n%s", got)
	}
}

// A session with no profile is plain AgentWorks and keeps its own layout.
func TestMultiAgentToolDescriptionKeepsAgentWorksLayoutWithoutProfile(t *testing.T) {
	const folder = "_users/default/Chats"

	shell := enhanceToolDescriptionForMultiAgentMode("execute_shell_command", "base.", folder, nil)
	for _, want := range []string{"{plan_id}"} {
		if !strings.Contains(shell, want) {
			t.Fatalf("AgentWorks description lost %q:\n%s", want, shell)
		}
	}

	// Read-only tools advertise only the generic writable chat scope.
	readOnly := enhanceToolDescriptionForMultiAgentMode("read_workspace_file", "base.", folder, nil)
	if !strings.Contains(readOnly, folder) {
		t.Fatalf("AgentWorks read-only description lost its chat scope:\n%s", readOnly)
	}
	productReadOnly := enhanceToolDescriptionForMultiAgentMode("read_workspace_file", "base.", folder, profileWithPlacement(nil))
	if strings.Contains(productReadOnly, "pulse/") {
		t.Fatalf("product read-only description advertises pulse/ write access:\n%s", productReadOnly)
	}
}
