package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/workproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/commands"
	workspacehandlers "github.com/manishiitg/coding-agent-loop/workspace/handlers"
	"github.com/spf13/viper"
)

const externalCommandCrewRoot = "Crew/test-team"

// Drive the MCP transport and the actual workspace file handlers against disk.
// This catches a tool that reports success but writes outside the app's project.
func newExternalCrewCommandFixture(t *testing.T) (*externalToolsFixture, string) {
	t.Helper()
	t.Setenv("MULTI_USER_MODE", "true")
	t.Setenv("AGENTWORKS_PROJECT_SHARING", "on")
	t.Setenv("AGENTWORKS_PRODUCTS_AVAILABLE_TO_ALL", "")
	t.Setenv("WORKSPACE_API_TOKEN", "crew-command-test-token")
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","role":"editor","products":["work"]},{"id":"other","role":"editor","products":["work"]}]}`)
	owners := withProjectOwnerRegistry(t)
	if err := owners.Register(projectOwnerRecord{Product: "work", Folder: "test-team", ProjectID: "team", OwnerID: "owner", Shared: true}); err != nil {
		t.Fatal(err)
	}
	f := &externalToolsFixture{docs: t.TempDir()}
	t.Setenv("WORKSPACE_DOCS_PATH", f.docs)
	oldDocs := viper.Get("docs-dir")
	viper.Set("docs-dir", f.docs)
	t.Cleanup(func() { viper.Set("docs-dir", oldDocs) })
	f.write(t, externalCommandCrewRoot+"/product.json", `{"schema_version":1,"product":"work","id":"team","title":"Test Team","session_id":"sess-team"}`)
	f.write(t, externalCommandCrewRoot+"/workflow.json", `{"id":"team","label":"Test Team","created_by":"owner"}`)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		if c.GetHeader("X-Workspace-Token") != "crew-command-test-token" {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		c.Next()
	})
	router.GET("/api/documents", workspacehandlers.ListDocuments)
	router.GET("/api/documents/*filepath", workspacehandlers.GetDocument)
	router.PUT("/api/documents/*filepath", workspacehandlers.UpdateDocument)
	router.POST("/api/folders", workspacehandlers.CreateFolder)
	router.DELETE("/api/folders/*folderpath", workspacehandlers.DeleteFolder)
	router.POST("/api/shared-file-write", workspacehandlers.SharedFileWrite)
	router.POST("/api/shared-file-lifecycle", workspacehandlers.SharedFileLifecycle)
	router.POST("/api/shared-assets", workspacehandlers.SharedAssets)
	router.POST("/api/dashboards", workspacehandlers.DashboardOperation)
	ws := httptest.NewServer(router)
	t.Cleanup(ws.Close)
	t.Setenv("WORKSPACE_API_URL", ws.URL)
	registry := agentprofiles.NewRegistry()
	profile := workproduct.BuiltinAgentProfile()
	profile.Product = "work" // Match the product binding applied at server startup.
	if err := registry.RegisterProfile(profile); err != nil {
		t.Fatal(err)
	}
	if err := workproduct.RegisterAgentProfileRuntime(registry, ws.URL); err != nil {
		t.Fatal(err)
	}
	f.api = &StreamingAPI{agentProfiles: registry}
	return f, ws.URL
}

func TestExternalCrewCommandsOverMCP(t *testing.T) {
	f, workspaceURL := newExternalCrewCommandFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	srv := serveExternalMCP(t, f.api, crewWriter("owner"))
	cli := dialExternalMCP(t, ctx, srv.URL+externalMCPPath)
	initializeExternalMCP(t, ctx, cli)
	call := func(args map[string]any) string {
		t.Helper()
		args["crew_id"] = "team"
		result := callRemoteTool(t, ctx, cli, "call_tool", map[string]any{"name": externalCrewCommandsTool, "arguments": args})
		requireRemoteSuccess(t, result, externalCrewCommandsTool)
		return marshalStructured(t, result)
	}
	call(map[string]any{"action": "create", "name": "review", "description": "Review work", "prompt": "Review this work.", "icon": "zap"})
	commandPath := externalCommandCrewRoot + "/commands/custom/review/COMMAND.md"
	if saved := f.read(t, commandPath); !strings.Contains(saved, "{{context}}") || !strings.Contains(saved, "Review this work.") {
		t.Fatalf("command did not use shared validation/storage: %s", saved)
	}
	if listed := call(map[string]any{"action": "list"}); !strings.Contains(listed, "review") {
		t.Fatalf("MCP cannot discover the saved command: %s", listed)
	}
	// The app slash menu reads the exact command created by MCP.
	w := httptest.NewRecorder()
	listCommandsHandler(workspaceURL)(w, adminRequest(http.MethodGet, "/commands?workspace_path="+externalCommandCrewRoot, "", crewWriter("owner"), nil))
	var menu commands.ListCommandsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &menu); err != nil || w.Code != 200 || menu.Total != 1 || menu.Commands[0].Frontmatter.Name != "review" {
		t.Fatalf("app slash menu = %d %s (%v)", w.Code, w.Body.String(), err)
	}
	call(map[string]any{"action": "update", "name": "review", "prompt": "Review changes."})
	if saved := f.read(t, commandPath); !strings.Contains(saved, "Review changes.") || !strings.Contains(saved, `icon: "zap"`) || !strings.Contains(saved, `description: "Review work"`) {
		t.Fatalf("command update was not stored: %s", saved)
	}
	// Product commands are not editable through this custom-only directory.
	productCommand := f.read(t, externalCommandCrewRoot+"/product.json")
	call(map[string]any{"action": "delete", "name": "review"})
	if _, err := os.Stat(filepath.Join(f.docs, commandPath)); !os.IsNotExist(err) {
		t.Fatalf("deleted command still exists: %v", err)
	}
	if f.read(t, externalCommandCrewRoot+"/product.json") != productCommand {
		t.Fatal("custom command operation changed the product manifest")
	}
}

func TestExternalCrewMemorySkillsAndCommandsFollowRoles(t *testing.T) {
	f, _ := newExternalCrewCommandFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	srv := serveExternalMCP(t, f.api, crewWriter("owner"))
	cli := dialExternalMCP(t, ctx, srv.URL+externalMCPPath)
	initializeExternalMCP(t, ctx, cli)
	for path, content := range map[string]string{
		"MEMORY.md":              "Remember the agreed process.\n",
		"skills/review/SKILL.md": "---\nname: review\ndescription: Review work\n---\nReview the work.\n",
	} {
		result := callRemoteTool(t, ctx, cli, "call_tool", map[string]any{"name": "files", "arguments": map[string]any{
			"action": "write", "crew_id": "team", "path": path, "content": content, "request_id": path,
		}})
		requireRemoteSuccess(t, result, "write "+path)
		if got := f.read(t, externalCommandCrewRoot+"/"+path); got != content {
			t.Fatalf("%s = %q, want %q", path, got, content)
		}
	}
	// Token consent never grants ownership or overrides a live account role.
	call := func(claims *UserClaims, name string, args map[string]any, want int) {
		t.Helper()
		body, err := json.Marshal(map[string]any{"name": name, "arguments": args})
		if err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		f.api.handleExternalCall(w, adminRequest(http.MethodPost, "/api/external/call", string(body), claims, nil))
		externalTestBody(t, w, want)
	}
	readOnlyToken := crewWriter("owner")
	readOnlyToken.AccessToken.Scopes = []string{"crews:read"}
	boundedToken := crewWriter("owner")
	boundedToken.AccessToken = &accesstokens.Token{Scopes: []string{"crews:read", "crews:write"}, CrewIDs: []string{"another-team"}}
	for _, denied := range []struct {
		claims     *UserClaims
		fileStatus int
	}{{crewWriter("other"), 403}, {readOnlyToken, 403}, {boundedToken, 404}} {
		for _, path := range []string{"MEMORY.md", "skills/review/SKILL.md"} {
			call(denied.claims, "files", map[string]any{"action": "write", "crew_id": "team", "path": path, "content": "Denied"}, denied.fileStatus)
		}
		call(denied.claims, externalCrewCommandsTool, map[string]any{"crew_id": "team", "action": "create", "name": "denied", "description": "Denied", "prompt": "Denied"}, 403)
	}
	call(crewWriter("other"), externalCrewCommandsTool, map[string]any{"crew_id": "team", "action": "list"}, 200)
	call(readOnlyToken, externalCrewCommandsTool, map[string]any{"crew_id": "team", "action": "list"}, 200)
	call(boundedToken, externalCrewCommandsTool, map[string]any{"crew_id": "team", "action": "list"}, 403)
	call(crewWriter("other"), "files", map[string]any{"action": "write", "crew_id": "team", "path": "shared/other/input.md", "content": "Input"}, 200)
	for _, role := range []string{"viewer", "disabled", "no-product"} {
		record := `{"id":"owner","role":"viewer","products":["work"]}`
		if role == "disabled" {
			record = `{"id":"owner","role":"editor","products":["work"],"disabled":true}`
		} else if role == "no-product" {
			record = `{"id":"owner","role":"editor","products":["code"]}`
		}
		withMemoryUserDirectory(t, `{"users":[`+record+`]}`)
		for _, path := range []string{"MEMORY.md", "skills/review/SKILL.md"} {
			call(crewWriter("owner"), "files", map[string]any{"action": "write", "crew_id": "team", "path": path, "content": "Denied"}, 403)
		}
		call(crewWriter("owner"), externalCrewCommandsTool, map[string]any{"crew_id": "team", "action": "create", "name": "denied", "description": "Denied", "prompt": "Denied"}, 403)
	}
	if got := f.read(t, externalCommandCrewRoot+"/MEMORY.md"); got != "Remember the agreed process.\n" {
		t.Fatalf("a denied write changed memory: %s", got)
	}
	if _, err := os.Stat(filepath.Join(f.docs, externalCommandCrewRoot, "commands/custom/denied/COMMAND.md")); !os.IsNotExist(err) {
		t.Fatalf("a denied write created a command: %v", err)
	}
}
