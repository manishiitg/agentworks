package server

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/livefeed"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/workproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/mediapresentation"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	workspacehandlers "github.com/manishiitg/coding-agent-loop/workspace/handlers"
	"net/http/httptest"
)

// Real workspace persistence -> shared media tool -> project live notice. A
// second result appears during the turn without changing the first collection.
func TestCrewMediaResultsPublishDuringTurn(t *testing.T) {
	f, _ := newExternalCrewCommandFixture(t)
	router := gin.New()
	router.GET("/api/documents/*filepath", workspacehandlers.HandleDocumentRequest)
	router.POST("/api/db/initialize", workspacehandlers.InitializeWorkflowDB)
	router.POST("/api/mutate", workspacehandlers.MutateWorkflowDB)
	router.POST("/api/query", workspacehandlers.QueryWorkflowDB)
	server := httptest.NewServer(router)
	defer server.Close()
	registry := agentprofiles.NewRegistry()
	if err := mediapresentation.Register(registry, server.URL); err != nil {
		t.Fatal(err)
	}
	var binding agentprofiles.ToolBinding
	for _, tool := range workproduct.BuiltinAgentProfile().Tools {
		if tool.ID == "media.show-character" {
			binding = tool
		}
	}
	if binding.Presentation == nil {
		t.Fatal("Crew does not declare shared media")
	}
	const session = "crew-media-test"
	workspace.SetSessionFolderGuard(session, []string{externalCommandCrewRoot}, []string{externalCommandCrewRoot})
	defer workspace.ClearSessionShellConfig(session)
	spec, err := registry.BuildTool(binding, agentprofiles.ToolRuntimeContext{UserID: "owner", SessionID: session, WorkspacePath: externalCommandCrewRoot, Presentation: binding.Presentation})
	if err != nil {
		t.Fatal(err)
	}
	feed := livefeed.Default.Subscribe()
	defer livefeed.Default.Unsubscribe(feed)
	for _, collection := range []string{"first", "second", "first"} {
		root := "db/assets/" + collection
		f.write(t, filepath.ToSlash(filepath.Join(externalCommandCrewRoot, root, "host.png")), "image evidence")
		f.write(t, filepath.ToSlash(filepath.Join(externalCommandCrewRoot, root, "host.md")), "# Host\nApproved character.")
		if _, err := spec.Execute(context.Background(), map[string]interface{}{"name": "Host", "image_path": root + "/host.png", "spec_path": root + "/host.md", "collection_id": collection}); err != nil {
			t.Fatal(err)
		}
	}
	client := workspace.NewClient(server.URL, workspace.WithUserID("owner"))
	rows, err := client.QueryWorkflowDB(context.Background(), workspace.QueryWorkflowDBParams{DBPath: externalCommandCrewRoot + "/db/db.sqlite", SQL: "SELECT revision,payload_json FROM ui_presentations"})
	if err != nil || len(rows) != 2 {
		t.Fatalf("saved results: %v %v", rows, err)
	}
	seen := map[string]bool{}
	for _, row := range rows {
		var payload map[string]interface{}
		if err := json.Unmarshal([]byte(row["payload_json"].(string)), &payload); err != nil {
			t.Fatal(err)
		}
		seen[payload["collection_id"].(string)] = true
	}
	if !seen["first"] || !seen["second"] {
		t.Fatalf("collection collision: %v", seen)
	}
	notices, _ := feed.Drain()
	found := false
	for _, notice := range notices {
		found = found || notice.Kind == livefeed.Report && notice.Workflow == externalCommandCrewRoot
	}
	if !found {
		t.Fatalf("no immediate dashboard notice: %v", notices)
	}
}
