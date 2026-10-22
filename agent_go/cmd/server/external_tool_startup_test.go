package server

import (
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/workproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"testing"
)

// The real startup registry parses embedded Builder schemas as well as the
// public catalog. A malformed schema must fail here before service startup.
func TestExternalToolCatalogInitializes(t *testing.T) {
	catalog, err := externalTools()
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog) == 0 {
		t.Fatal("startup tool catalog is empty")
	}
	// Being in the schema catalog is insufficient: Crew integrations also filters
	// tools through the resolved product feature policy.
	profile := workproduct.BuiltinAgentProfile()
	if err := agentprofiles.ResolveFeatures(&profile); err != nil {
		t.Fatal(err)
	}
	if !containsExactString(profile.ToolPolicy.Enabled, "configure_agent_inbox") {
		t.Fatal("Crew inbox configuration is missing from the live integration tool policy")
	}
}
