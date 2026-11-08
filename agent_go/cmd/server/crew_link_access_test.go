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

	"github.com/gin-gonic/gin"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	workspacehandlers "github.com/manishiitg/coding-agent-loop/workspace/handlers"
	"github.com/spf13/viper"
)

// PLAT-746-471: reach the current private-Crew rule with a real declared
// entry, then grant and revoke access without rebuilding the caller's tools.
func TestDeclaredCrewEntryAndLinksRecheckCurrentAccess(t *testing.T) {
	for _, surface := range []string{"call", "link"} {
		t.Run(surface, func(t *testing.T) {
			t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
			env := newCrewFunctionEnv(t)
			ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner"})
			ownerCtx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "other"})
			target, err := resolveTriggerTarget(ownerCtx, &UserClaims{UserID: "other"}, "Gamma")
			if err != nil {
				t.Fatal(err)
			}
			fn := savedFunctionForTest(t, ownerCtx, target, crewFunction{Name: "access_task", Description: "Access task", Instructions: "Complete the task."})
			links := &recordingRegistrar{}
			if err := env.api.registerWorkWorkflowReferenceTools(links, "owner", "link-owner", linkAlphaPath, false); err != nil {
				t.Fatal(err)
			}
			run := &recordingRegistrar{}
			if err := env.api.registerWorkWorkflowReferenceTools(run, "other", "link-run", linkAlphaPath, true); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"attach_workflow_reference", "detach_workflow_reference"} {
				if _, ok := run.tools[name]; ok {
					t.Fatalf("Run person received mutating tool %s", name)
				}
			}
			if _, err := run.tools["list_accessible_workflows"].exec(context.Background(), map[string]interface{}{}); err != nil {
				t.Fatalf("Run discovery refused: %v", err)
			}
			setAudience := func(people []string) {
				t.Helper()
				if err := defaultProjectOwners().SetAudience("work", "gamma", true, people); err != nil {
					t.Fatal(err)
				}
			}
			invoke := func() (string, error) {
				if surface == "link" {
					return links.tools["attach_workflow_reference"].exec(ctx, map[string]interface{}{"workspace_path": linkGammaPath})
				}
				return env.alpha["project_functions_call"].exec(ctx, map[string]interface{}{"project": "Gamma", "function": fn.Name, "wait_seconds": 0, "notify": false})
			}
			denied := func(stage string) {
				t.Helper()
				ref, ok := resolveCrewPath(ctx, "owner", linkGammaPath)
				if !ok || crewAccessFor(&UserClaims{UserID: "owner"}, ref) != crewAccessNone {
					t.Fatal("private target fixture is accessible")
				}
				_, err := invoke()
				if err == nil || (!strings.Contains(err.Error(), "no Crew or workflow you can use") && !strings.Contains(err.Error(), "no longer have access") && !strings.Contains(err.Error(), "access denied")) {
					t.Errorf("%s %s admitted private target or missed intended access rule: %v", stage, surface, err)
				}
			}
			setAudience(nil)
			denied("initial")
			setAudience([]string{"owner"})
			out, err := invoke()
			if err != nil {
				t.Fatalf("shared %s refused: %v", surface, err)
			}
			if surface == "call" {
				view := decodeToolJSON(t, out)
				id, _ := view["call_id"].(string)
				call := lookupCrewFunctionCall(id)
				if call == nil {
					t.Fatal("allowed call has no record")
				}
				defer call.finish("completed", "fixture", "")
				if call.RunID == "" || call.TriggerID != fn.TriggerID {
					t.Fatalf("allowed call never reached declared dispatch: %+v", view)
				}
			} else {
				refs, err := readWorkWorkflowReferences(ctx, linkAlphaPath)
				if err != nil || !containsWorkReferencePath(refs, linkGammaPath) {
					t.Fatalf("allowed link not saved: %v %v", refs, err)
				}
			}
			setAudience(nil)
			denied("revoked")
		})
	}
}

// Exercise the actual linked session guard, workspace client and HTTP document
// reader with synthetic bytes. The same client must lose access after revocation.
func TestLinkedPrivateCrewFileReadChecksLiveAccess(t *testing.T) {
	docs := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	env := newCrewFunctionEnv(t)
	oldDocs := viper.Get("docs-dir")
	viper.Set("docs-dir", docs)
	t.Cleanup(func() { viper.Set("docs-dir", oldDocs) })
	const sid = "linked-private-file-proof"
	const file = linkGammaPath + "/linked-proof.txt"
	const marker = "SYNTHETIC_PRIVATE_TARGET_BYTES"
	if err := os.WriteFile(filepath.Join(docs, filepath.FromSlash(file)), []byte(marker), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(docs, linkGammaPath, "skills/context"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docs, linkGammaPath, "skills/context/SKILL.md"), []byte(marker), 0600); err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/api/documents/*filepath", workspacehandlers.GetDocument)
	router.GET("/api/documents", workspacehandlers.ListDocuments)
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, r)
		t.Logf("workspace request method=%s path=%s caller=%s status=%d synthetic_marker_returned=%v", r.Method, r.URL.Path, r.Header.Get("X-User-ID"), recorder.Code, strings.Contains(recorder.Body.String(), marker))
		for key, values := range recorder.Header() {
			for _, v := range values {
				w.Header().Add(key, v)
			}
		}
		w.WriteHeader(recorder.Code)
		_, _ = w.Write(recorder.Body.Bytes())
	}))
	defer httpServer.Close()
	common.SetSessionFolderGuard(sid, []string{linkAlphaPath}, []string{linkAlphaPath})
	t.Cleanup(func() { common.ClearSessionShellConfig(sid) })
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner"})
	ctx = context.WithValue(ctx, common.ChatSessionIDKey, sid)
	links := &recordingRegistrar{}
	if err := env.api.registerWorkWorkflowReferenceTools(links, "owner", sid, linkAlphaPath, false); err != nil {
		t.Fatal(err)
	}
	client := workspace.NewClient(httpServer.URL, workspace.WithUserID("owner"))
	setAudience := func(people []string) {
		t.Helper()
		if err := defaultProjectOwners().SetAudience("work", "gamma", true, people); err != nil {
			t.Fatal(err)
		}
	}
	attach := func() error {
		_, err := links.tools["attach_workflow_reference"].exec(ctx, map[string]interface{}{"workspace_path": linkGammaPath})
		return err
	}
	read := func(want bool, stage string) {
		t.Helper()
		for _, path := range []string{file, linkGammaPath + "/skills/context/SKILL.md"} {
			out, err := client.ReadWorkspaceFile(ctx, workspace.ReadWorkspaceFileParams{Filepath: path})
			if want {
				if err != nil || out.Content != marker {
					t.Fatalf("%s allowed read: content=%q err=%v", stage, out.Content, err)
				}
			} else if err == nil || out.Content != "" {
				t.Errorf("%s private linked read returned target bytes=%v err=%v", stage, out.Content == marker, err)
			}
		}
	}
	// Native MCP read/list/search already have their own live admission, which
	// must remain consistent with linked context and ordinary file tools.
	env.api.agentProfiles = env.svc.registry
	mcpClaims := &UserClaims{UserID: "owner", AccessToken: &accesstokens.Token{Scopes: []string{"crews:read"}, AllCrews: true}}
	mcpRead := func(want bool, stage string) {
		t.Helper()
		for _, tool := range []string{"read_crew_file", "list_crew_files", "search_crew_files"} {
			args := map[string]any{"crew_id": "gamma", "query": marker}
			if tool == "read_crew_file" {
				args["path"] = "linked-proof.txt"
			}
			code, out := externalCrewRequest(t, env.triggerLinkEnv, mcpClaims, tool, args)
			raw, _ := json.Marshal(out)
			t.Logf("MCP tool=%s stage=%s status=%d", tool, stage, code)
			if want {
				if code != http.StatusOK {
					t.Errorf("allowed MCP %s: %d %s", tool, code, raw)
				}
			} else if code != http.StatusForbidden && code != http.StatusNotFound {
				t.Errorf("private MCP %s: %d %s", tool, code, raw)
			}
		}
	}
	list := func(want bool, stage string) {
		t.Helper()
		request := &QueryRequest{WorkflowContextPaths: []string{linkGammaPath}}
		if err := admitTurnContextPaths(ctx, request); err != nil {
			t.Fatalf("context refresh: %v", err)
		}
		if (len(request.authorizedWorkflowContextReadPaths) > 0) != want {
			t.Errorf("%s linked context admitted=%v", stage, request.authorizedWorkflowContextReadPaths)
		}
		out, err := client.ListWorkspaceFiles(ctx, workspace.ListWorkspaceFilesParams{Folder: linkGammaPath})
		if want {
			if err != nil || !strings.Contains(string(out.Raw), "linked-proof") {
				t.Errorf("allowed list %s: %s %v", stage, out.Raw, err)
			}
		} else if err == nil {
			t.Errorf("private list %s succeeded", stage)
		}
	}

	setAudience(nil)
	if err := attach(); err == nil {
		t.Error("initial private Crew link was accepted")
	}
	read(false, "initial")
	list(false, "initial")
	mcpRead(false, "initial")
	setAudience([]string{"owner"})
	if err := attach(); err != nil {
		t.Fatalf("shared link refused: %v", err)
	}
	read(true, "shared")
	list(true, "shared")
	mcpRead(true, "shared")
	setAudience(nil)
	read(false, "revoked same client/session")
	list(false, "revoked same client/session")
	mcpRead(false, "revoked")
	// Go, CLI and browser shell tools use the same refreshed session guard on
	// their next request. Exercise that authenticated binder rather than manually
	// deleting its grant in the fixture.
	bind := env.api.bindToolExecutionContext(ctx, sid, QueryRequest{AgentProfileID: "work", AgentProfileConversationKey: "alpha", SelectedFolder: linkAlphaPath}, false)
	if _, err := bind(context.Background(), "execute_shell_command"); err != nil {
		t.Fatalf("own Crew tool refused after linked audience removal: %v", err)
	}
	cfg := common.GetSessionShellConfig(sid)
	for _, paths := range [][]string{cfg.ReadPaths, cfg.WritePaths} {
		if containsWorkReferencePath(paths, linkGammaPath) {
			t.Error("revoked Crew remains in next shell mount grants")
		}
	}
	if !containsWorkReferencePath(cfg.ReadPaths, linkAlphaPath) {
		t.Error("revoking link removed own Crew")
	}

	// Its owner can still read the same real file after the other person loses access.
	ownerCtx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "other"})
	ownerClient := workspace.NewClient(httpServer.URL, workspace.WithUserID("other"), workspace.WithFolderGuard(&workspace.FolderGuardConfig{Enabled: true, ReadPaths: []string{linkGammaPath}}))
	if out, err := ownerClient.ReadWorkspaceFile(ownerCtx, workspace.ReadWorkspaceFileParams{Filepath: file}); err != nil || out.Content != marker {
		t.Fatalf("target owner read: %q %v", out.Content, err)
	}
}

// A Crew-shaped path needs a registered current person, even when an old owner
// lookup was primed before the server's authoritative owner record changed.
func TestCrewFilePathPolicyRequiresCurrentRegisteredPerson(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	newTriggerLinkEnv(t)
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner"})
	if err := requireCurrentCrewPathAccess(ctx, linkAlphaPath); err != nil {
		t.Fatalf("registered owner refused: %v", err)
	}
	for _, path := range []string{linkAlphaPath, "Crew/unknown/file.txt", "Crew/.hidden/file.txt", "Crew", `Crew\alpha/file.txt`, "else/../Crew/alpha/file.txt", filepath.Join(os.Getenv("WORKSPACE_DOCS_PATH"), linkAlphaPath, "file.txt")} {
		if err := requireCurrentCrewPathAccess(context.Background(), path); err == nil {
			t.Errorf("missing person admitted %s", path)
		}
	}
	if err := requireCurrentCrewPathAccess(ctx, "Crew/unknown/file.txt"); err == nil {
		t.Error("unknown Crew folder admitted")
	}
	if err := requireCurrentCrewPathAccess(context.Background(), "runtime/system-output.txt"); err != nil {
		t.Fatalf("unrelated system path refused: %v", err)
	}
	if err := defaultProjectOwners().modify(func(records map[string]projectOwnerRecord) (bool, error) {
		rec := records[projectOwnerKey("work", "alpha")]
		rec.OwnerID = "other"
		rec.Private = true
		rec.AudienceConfigured = true
		rec.RunPeople = nil
		records[projectOwnerKey("work", "alpha")] = rec
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := requireCurrentCrewPathAccess(ctx, linkAlphaPath); err == nil {
		t.Error("former owner retained cached access after transfer")
	}
	next := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "other"})
	if err := requireCurrentCrewPathAccess(next, linkAlphaPath); err != nil {
		t.Fatalf("current owner refused: %v", err)
	}
}
