package admin

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/mcp-gateway/internal/store"
)

// "What can this person reach" answers with the same check as a real call
// and names the group that gives each tool; membership changes are in the
// access history with who made them.
func TestInspectUserAndMemberHistory(t *testing.T) {
	st := store.NewMemoryStore()
	st.AddWorkspace(store.Workspace{ID: "w"})
	st.AddUser(store.User{ID: "priya", WorkspaceID: "w"})
	st.AddGroup(store.Group{ID: "sales", WorkspaceID: "w", Name: "Sales"})
	st.AddConnector(store.Connector{ID: "c", WorkspaceID: "w", Label: "CRM", Provider: "crm", Status: store.StatusActive, UpstreamURL: "https://crm.example.com/mcp"})
	for _, name := range []string{"crm__search", "crm__delete"} {
		tool := st.UpsertToolSnapshot(store.ToolSnapshot{WorkspaceID: "w", ConnectorID: "c", PublicName: name, Fingerprint: "f", InputSchema: []byte(`{"type":"object"}`)})
		st.ApproveTool("w", tool.PublicName, tool.Fingerprint, tool.Version)
	}
	st.AddGroupGrant(store.GroupGrant{GroupID: "sales", PublicName: "crm__search"})
	a := &Admin{Store: st, WorkspaceID: "w"}
	if err := a.SetMember("sales", "priya", true, "admin-1"); err != nil {
		t.Fatal(err)
	}
	out, err := a.setupTool(t.Context(), "inspect_user", json.RawMessage(`{"user_id":"priya"}`))
	if err != nil {
		t.Fatal(err)
	}
	result := out.(map[string]any)
	tools := result["tools"].([]userToolAccess)
	if result["allowed_tool_count"] != 1 || len(tools) != 1 || tools[0].PublicName != "crm__search" || !tools[0].Allowed || tools[0].Via[0] != "sales" {
		t.Fatalf("inspect_user: %+v", result)
	}
	// Access is group-only: no new direct grant; an old one still shows, marked.
	if err := a.SetUserGrant("priya", "crm__delete", true); !errors.Is(err, ErrGroupOnlyAccess) {
		t.Fatalf("direct grant accepted: %v", err)
	}
	st.AddGrant(store.Grant{UserID: "priya", PublicName: "crm__delete"})
	out, err = a.setupTool(t.Context(), "inspect_user", json.RawMessage(`{"user_id":"priya"}`))
	if err != nil {
		t.Fatal(err)
	}
	tools = out.(map[string]any)["tools"].([]userToolAccess)
	if len(tools) != 2 || tools[0].PublicName != "crm__delete" || !strings.Contains(tools[0].Via[0], "old") {
		t.Fatalf("old direct grant not shown: %+v", tools)
	}
	events := st.ListPolicyEvents("w")
	last := events[len(events)-1]
	if last.Action != "add_member" || last.Actor != "admin-1" || last.GroupID != "sales" || last.UserID != "priya" {
		t.Fatalf("membership change not in history: %+v", last)
	}
}
