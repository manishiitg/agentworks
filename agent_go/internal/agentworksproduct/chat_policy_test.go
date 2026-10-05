package agentworksproduct

import (
	"reflect"
	"testing"
)

func TestChatPolicyManifestAuthority(t *testing.T) {
	for _, mode := range []string{"builder", "run"} {
		for _, origin := range []string{"interactive", "scheduled", "pulse", "child", "bot", "notification", "unknown"} {
			for _, readOnly := range []bool{false, true} {
				got := ChatCapabilities(mode, origin, readOnly)
				want := mode == "builder" && (origin == "interactive" || origin == "bot" || origin == "notification") && !readOnly
				if got["mcp_management"] != want || got["user_management"] != want {
					t.Fatalf("MCP admission %s/%s readOnly=%v: %v", mode, origin, readOnly, got)
				}
				if readOnly && got["plan_authoring"] {
					t.Fatal("read-only user can author")
				}
			}
		}
	}
}

// Run mode (every reader, every Slack channel turn) lists MCP servers
// without managing them; scheduled, Pulse and child agents do neither.
func TestMCPInspectionIsDeclaredForRunMode(t *testing.T) {
	for _, mode := range []string{"builder", "run"} {
		for _, readOnly := range []bool{false, true} {
			for _, origin := range []string{"interactive", "bot"} {
				if !ChatCapabilities(mode, origin, readOnly)["mcp_inspection"] {
					t.Fatalf("%s/%s readOnly=%v cannot list MCP servers", mode, origin, readOnly)
				}
			}
			for _, origin := range []string{"scheduled", "pulse", "child"} {
				if ChatCapabilities(mode, origin, readOnly)["mcp_inspection"] {
					t.Fatalf("%s admitted undeclared MCP inspection", origin)
				}
			}
		}
	}
}

func TestScheduledAndPulseShareUnattendedCapabilities(t *testing.T) {
	for _, mode := range []string{"builder", "run"} {
		for _, readOnly := range []bool{false, true} {
			scheduled := ChatCapabilities(mode, "scheduled", readOnly)
			pulse := ChatCapabilities(mode, "pulse", readOnly)
			if !reflect.DeepEqual(scheduled, pulse) {
				t.Fatalf("%s readOnly=%v scheduled=%v pulse=%v", mode, readOnly, scheduled, pulse)
			}
			for _, capability := range []string{"user_management", "mcp_management", "mcp_inspection", "bot_management", "workspace_ui"} {
				if scheduled[capability] {
					t.Fatalf("unattended %s admitted %s", mode, capability)
				}
			}
			if !scheduled["workflow_suggestions"] {
				t.Fatalf("unattended %s readOnly=%v cannot submit suggestions", mode, readOnly)
			}
			for _, capability := range []string{"plan_authoring", "report_authoring", "secret_management", "knowledgebase_maintenance", "improvement_proposals"} {
				if scheduled[capability] != (mode == "builder" && !readOnly) {
					t.Fatalf("unattended %s readOnly=%v unexpected %s admission", mode, readOnly, capability)
				}
			}
		}
	}
}

func TestChatPolicyManifestRejectsUnknownCapabilities(t *testing.T) {
	m, err := AgentWorksManifest()
	if err != nil {
		t.Fatal(err)
	}
	// Copy policy/maps: never mutate the cached product manifest.
	p := *m.ChatPolicy
	p.Modes = map[string][]string{"builder": {"mcp_managment"}, "run": {}}
	m.ChatPolicy = &p
	if validateChatPolicy(m) == nil {
		t.Fatal("typo silently accepted")
	}
}

func TestBotManagementAdmissionIsDeclaredByProductManifest(t *testing.T) {
	for _, mode := range []string{"builder", "run"} {
		for _, readOnly := range []bool{false, true} {
			if !ChatCapabilities(mode, "interactive", readOnly)["bot_management"] {
				t.Fatal("interactive bot inspection was not declared")
			}
			if ChatCapabilities(mode, "pulse", readOnly)["bot_management"] {
				t.Fatal("pulse admitted undeclared bot management")
			}
		}
	}
}
