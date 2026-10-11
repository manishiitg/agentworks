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

	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/mcpclient"
)

func crewCapabilityHTTP(t *testing.T, f *externalToolsFixture, claims *UserClaims, name string, args map[string]any, status int) string {
	t.Helper()
	args["crew_id"] = "team"
	body, _ := json.Marshal(map[string]any{"name": name, "arguments": args})
	w := httptest.NewRecorder()
	f.api.handleExternalCall(w, adminRequest(http.MethodPost, "/api/external/call", string(body), claims, nil))
	externalTestBody(t, w, status)
	return w.Body.String()
}
func TestExternalCrewFileLifecycleOverMCP(t *testing.T) {
	f, _ := newExternalCrewCommandFixture(t)
	f.write(t, externalCommandCrewRoot+"/code/report.md", "Old text\n")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	srv := serveExternalMCP(t, f.api, crewWriter("owner"))
	cli := dialExternalMCP(t, ctx, srv.URL+externalMCPPath)
	initializeExternalMCP(t, ctx, cli)
	call := func(args map[string]any) {
		t.Helper()
		args["crew_id"] = "team"
		result := callRemoteTool(t, ctx, cli, "call_tool", map[string]any{"name": "crew_files", "arguments": args})
		requireRemoteSuccess(t, result, "crew_files")
	}
	patch := map[string]any{"action": "patch", "path": "code/report.md", "diff": "--- a/report.md\n+++ b/report.md\n@@ -1 +1 @@\n-Old text\n+New text\n", "expected_revision": wf.Revision([]byte("Old text\n")), "request_id": "patch-report"}
	call(patch)
	call(patch)
	if got := f.read(t, externalCommandCrewRoot+"/code/report.md"); got != "New text\n" {
		t.Fatalf("patch/retry = %q", got)
	}
	call(map[string]any{"action": "mkdir", "path": "archive", "expected_revision": "missing"})
	call(map[string]any{"action": "move", "path": "code/report.md", "destination": "archive/report.md", "expected_revision": wf.Revision([]byte("New text\n"))})
	if got := f.read(t, externalCommandCrewRoot+"/archive/report.md"); got != "New text\n" {
		t.Fatal(got)
	}
	call(map[string]any{"action": "delete", "path": "archive/report.md", "expected_revision": wf.Revision([]byte("New text\n"))})
	call(map[string]any{"action": "delete", "path": "archive"})
	f.write(t, externalCommandCrewRoot+"/code/locked/keep.md", "Keep")
	guarded := crewWriter("owner")
	guarded.AccessToken.FileGuard = &wf.FolderGuard{ReadPaths: []string{"."}, WritePaths: []string{"code", "archive"}, ReadOnlyPaths: []string{"code/locked"}}
	crewCapabilityHTTP(t, f, guarded, "crew_files", map[string]any{"action": "move", "path": "code", "destination": "archive"}, 403)
	crewCapabilityHTTP(t, f, guarded, "crew_files", map[string]any{"action": "delete", "path": "code/locked/keep.md", "expected_revision": wf.Revision([]byte("Keep"))}, 403)
	crewCapabilityHTTP(t, f, guarded, "files", map[string]any{"action": "write", "path": "code/locked/keep.md", "content": "Denied"}, 403)
	guarded.AccessToken.FileGuard.BlockedPaths = []string{"code/locked"}
	crewCapabilityHTTP(t, f, guarded, "files", map[string]any{"action": "read", "path": "code/locked/keep.md"}, 403)

	crewCapabilityHTTP(t, f, crewWriter("owner"), "crew_files", map[string]any{"action": "delete", "path": "code"}, 409)
	f.write(t, externalCommandCrewRoot+"/code/functions.json", "{}")
	crewCapabilityHTTP(t, f, crewWriter("owner"), "crew_files", map[string]any{"action": "move", "path": "code", "destination": "archive"}, 403)
	for _, denied := range []*UserClaims{crewWriter("other"), crewReader("owner")} {
		crewCapabilityHTTP(t, f, denied, "crew_files", map[string]any{"action": "mkdir", "path": "denied", "expected_revision": "missing"}, 403)
	}
	crewCapabilityHTTP(t, f, crewWriter("owner"), "crew_files", map[string]any{"action": "delete", "path": "../workflow.json"}, 403)
	crewCapabilityHTTP(t, f, crewWriter("owner"), "crew_files", map[string]any{"action": "patch", "path": "code/locked/keep.md", "diff": "bad", "request_id": "bad", "expected_revision": "stale"}, 409)
}
func crewReader(user string) *UserClaims {
	claims := crewWriter(user)
	claims.AccessToken.Scopes = []string{"crews:read"}
	return claims
}

func TestExternalCrewConnectionsAndReferenceBounds(t *testing.T) {
	f, _ := newExternalCrewCommandFixture(t)
	t.Setenv("PUBLIC_URL", "")
	catalogPath := filepath.Join(t.TempDir(), "mcp.json")
	catalog := `{"mcpServers":{"Example":{"url":"https://mcp.example.com/mcp","protocol":"http","oauth":{"auth_url":"https://auth.example.com/authorize","token_url":"https://auth.example.com/token"}}}}`
	if err := os.WriteFile(catalogPath, []byte(catalog), 0600); err != nil {
		t.Fatal(err)
	}
	f.api.mcpConfigPath = catalogPath
	f.api.logger = loggerv2.NewNoop()
	config, err := mcpclient.LoadConfig(catalogPath, f.api.logger)
	if err != nil {
		t.Fatal(err)
	}
	f.api.mcpConfig = config
	result := crewCapabilityHTTP(t, f, crewWriter("owner"), "crew_mcp", map[string]any{"action": "install", "name": "Example"}, 200)
	if !strings.Contains(result, "Integrations") {
		t.Fatal(result)
	}
	attachments, _ := placeMCPAttachmentsFor(externalCommandCrewRoot)
	if len(attachments) != 1 {
		t.Fatalf("attachments=%v", attachments)
	}
	// The same personal login is used elsewhere; removing the Crew attachment
	// must leave that other attachment and the login untouched.
	otherRoot := "_users/owner/Chats/Work/projects/another"
	if err = attachPersonalLoginToPlace("owner", attachments[0].Server, otherRoot); err != nil {
		t.Fatal(err)
	}
	crewCapabilityHTTP(t, f, crewReader("other"), "crew_mcp", map[string]any{"action": "list"}, 200)
	crewCapabilityHTTP(t, f, crewWriter("other"), "crew_mcp", map[string]any{"action": "remove", "name": attachments[0].Server}, 403)
	crewCapabilityHTTP(t, f, crewWriter("owner"), "crew_mcp", map[string]any{"action": "install", "name": "Example", "client_secret": "denied"}, 400)
	crewCapabilityHTTP(t, f, crewWriter("owner"), "crew_mcp", map[string]any{"action": "remove", "name": attachments[0].Server}, 200)
	current, _ := placeMCPAttachmentsFor(externalCommandCrewRoot)
	other, _ := placeMCPAttachmentsFor(otherRoot)
	personal, _ := listPlaceMCPServers("owner")
	if len(current) != 0 || len(other) != 1 || len(personal) != 1 {
		t.Fatalf("detach changed other state: %v %v %v", current, other, personal)
	}
	f.write(t, "Workflow/research/workflow.json", `{"id":"research","label":"Research","access":{"owners":["owner"]}}`)
	claims := crewWriter("owner")
	claims.AccessToken.Scopes = append(claims.AccessToken.Scopes, "workflows:read", "files:read")
	claims.AccessToken.AllWorkflows = true
	result = crewCapabilityHTTP(t, f, claims, "crew_references", map[string]any{"action": "list"}, 200)
	if !strings.Contains(result, "Workflow/research") {
		t.Fatal(result)
	}
	crewCapabilityHTTP(t, f, claims, "crew_references", map[string]any{"action": "attach", "workspace_path": "Workflow/research"}, 200)
	if !strings.Contains(f.read(t, externalCommandCrewRoot+"/workflow.json"), "Workflow/research") {
		t.Fatal("reference was not persisted")
	}
	crewCapabilityHTTP(t, f, claims, "crew_references", map[string]any{"action": "detach", "workspace_path": "Workflow/research"}, 200)
	claims.AccessToken.AllWorkflows = false
	claims.AccessToken.WorkflowIDs = []string{"other"}
	crewCapabilityHTTP(t, f, claims, "crew_references", map[string]any{"action": "attach", "workspace_path": "Workflow/research"}, 403)
	result = crewCapabilityHTTP(t, f, claims, "crew_references", map[string]any{"action": "list"}, 200)
	if strings.Contains(result, "Workflow/research") {
		t.Fatal("out-of-bound reference was listed")
	}
	for _, group := range []string{"crew_skills", "crew_mcp", "crew_folders", "crew_references"} {
		action := map[string]string{"crew_skills": "uninstall", "crew_mcp": "remove", "crew_folders": "attach", "crew_references": "attach"}[group]
		crewCapabilityHTTP(t, f, crewReader("owner"), group, map[string]any{"action": action}, 403)
		crewCapabilityHTTP(t, f, crewWriter("other"), group, map[string]any{"action": action}, 403)
	}
}
func TestExternalCrewAccountFoldersRequireBroadConsent(t *testing.T) {
	f, _ := newExternalCrewCommandFixture(t)
	root := t.TempDir()
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	document, _ := json.Marshal(map[string]any{"assignments": map[string]any{"owner": map[string]any{"roots": []string{canonical}}}, "grants": map[string]any{}})
	f.write(t, workFolderAccessFilePath(), string(document))
	args := func() map[string]any {
		return map[string]any{"action": "attach", "path": root, "alias": "references", "access": "read_only"}
	}
	bounded := crewWriter("owner")
	bounded.AccessToken.AllCrews = false
	bounded.AccessToken.CrewIDs = []string{"team"}
	crewCapabilityHTTP(t, f, bounded, "crew_folders", args(), 403)
	guarded := crewWriter("owner")
	guarded.AccessToken.FileGuard = &wf.FolderGuard{WritePaths: []string{"."}}
	crewCapabilityHTTP(t, f, guarded, "crew_folders", args(), 403)
	crewCapabilityHTTP(t, f, crewWriter("owner"), "crew_folders", args(), 200)
	result := crewCapabilityHTTP(t, f, crewWriter("owner"), "crew_folders", map[string]any{"action": "list"}, 200)
	if !strings.Contains(result, "references") {
		t.Fatal(result)
	}
	grants := workFolderGrantsForClaims(context.Background(), crewWriter("owner"))
	if len(grants) != 1 {
		t.Fatalf("grants=%v", grants)
	}
	crewCapabilityHTTP(t, f, crewWriter("owner"), "crew_folders", map[string]any{"action": "detach", "id": grants[0].ID}, 200)
	if grants = workFolderGrantsForClaims(context.Background(), crewWriter("owner")); len(grants) != 0 {
		t.Fatalf("detach left grants=%v", grants)
	}
	outside := args()
	outside["path"] = t.TempDir()
	crewCapabilityHTTP(t, f, crewWriter("owner"), "crew_folders", outside, 400)
}
