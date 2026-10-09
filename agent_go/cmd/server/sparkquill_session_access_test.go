package server

import (
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/sparkquillproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

func TestSparkQuillSessionAccessNotice(t *testing.T) {
	profile := &resolvedAgentProfile{Definition: agentprofiles.Profile{ID: sparkquillproduct.ParentProfileID}}
	notice := agentSessionModeForTurn(QueryRequest{}, "default", "", profile, false)
	if !strings.Contains(notice, "execute_shell_command") || !strings.Contains(notice, "actual folder and access permissions") {
		t.Fatalf("missing platform write guidance: %s", notice)
	}
	if sent := withSessionMode(notice, "Create a lesson"); stripSessionMode(sent) != "Create a lesson" || withSessionMode(notice, sent) != sent {
		t.Fatal("access notice must reach retained input without changing display text or duplicating")
	}
	profile.Definition.Runtime.AgentTools.Mode = "full"
	if fullNotice := agentSessionModeForTurn(QueryRequest{}, "default", "", profile, false); !strings.Contains(fullNotice, "full native tools") || !strings.Contains(fullNotice, "within the granted workspace") {
		t.Fatalf("full native tools guidance missing: %s", fullNotice)
	}
	if got := agentSessionModeForTurn(QueryRequest{}, "default", "", profile, true); got != "" {
		t.Fatalf("read-only caller received write guidance: %s", got)
	}
	profile.Definition.ID = sparkquillproduct.ChildProfileID
	if got := agentSessionModeForTurn(QueryRequest{}, "default", "", profile, false); got != "" {
		t.Fatalf("child received parent write guidance: %s", got)
	}
}
