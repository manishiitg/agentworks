package step_based_workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	workspaceclient "github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	workspacehandlers "github.com/manishiitg/coding-agent-loop/workspace/handlers"
	"github.com/spf13/viper"
)

// Exercise the real agent guard, guarded patch/read client and
// workspace HTTP handlers. All writes are isolated in a temporary docs root;
// no live research, browser, or workflow database is touched. This is a file
// transport test, not a claim to exercise the Linux/macOS shell sandbox.
func TestAgentResearchPacketsPersistInsideGrantedAssets(t *testing.T) {
	root := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", root)
	previous := viper.Get("docs-dir")
	viper.Set("docs-dir", root)
	t.Cleanup(func() { viper.Set("docs-dir", previous) })
	router := gin.New()
	router.PATCH("/api/documents/*filepath", workspacehandlers.HandleDocumentRequest)
	router.GET("/api/documents/*filepath", workspacehandlers.HandleDocumentRequest)
	server := httptest.NewServer(router)
	defer server.Close()
	client := workspaceclient.NewClient(server.URL)
	hcpo := newAgentClosingTestOrchestrator(t)
	hcpo.selectedRunFolder = "iteration-0/default"
	workflow := hcpo.GetWorkspacePath()
	for _, source := range []string{"reddit", "x_twitter", "hackernews", "websearch"} {
		t.Run(source, func(t *testing.T) {
			sessionID := "durable-packet-" + source
			reads, writes := hcpo.setupAgentFolderGuard("parent/agents/"+source+"/calls/call-1", source, nil, AgentWriteAccess{})
			common.SetSessionFolderGuard(sessionID, reads, writes)
			configureWorkflowDBSession(sessionID, workflow, DBAccessReadWrite, false)
			t.Cleanup(func() { common.ClearSessionShellConfig(sessionID) })
			ctx := context.WithValue(context.Background(), common.ChatSessionIDKey, sessionID)
			path := workflow + "/db/assets/research/2026-09-05-" + source + ".json"
			body := fmt.Sprintf(`{"source":%q,"finding_count":1,"findings":[{"title":"fixture"}]}`, source)
			diff := "--- /dev/null\n+++ b/packet.json\n@@ -0,0 +1 @@\n+" + body + "\n"
			if _, err := client.DiffPatchWorkspaceFile(ctx, workspaceclient.DiffPatchWorkspaceFileParams{Filepath: path, Diff: diff}); err != nil {
				t.Fatalf("durable write through production client/handler: %v", err)
			}
			data, err := client.DownloadFile(ctx, path)
			if err != nil {
				t.Fatalf("guarded readback: %v", err)
			}
			var packet struct {
				Source   string            `json:"source"`
				Count    int               `json:"finding_count"`
				Findings []json.RawMessage `json:"findings"`
			}
			if err := json.Unmarshal(data, &packet); err != nil || packet.Source != source || packet.Count != len(packet.Findings) || packet.Count != 1 {
				t.Fatalf("invalid persisted packet: %s (decode error %v)", data, err)
			}
			for _, denied := range []string{workflow + "/db/research/packet.json", workflow + "/db/db.sqlite", workflow + "/db/db.sqlite-wal"} {
				if _, err := client.DiffPatchWorkspaceFile(ctx, workspaceclient.DiffPatchWorkspaceFileParams{Filepath: denied, Diff: diff}); err == nil {
					t.Fatalf("unsupported or raw DB write allowed: %s", denied)
				}
				if _, err := os.Stat(filepath.Join(root, denied)); !os.IsNotExist(err) {
					t.Fatalf("denied write created a file: %s (%v)", denied, err)
				}
			}
		})
	}
}
