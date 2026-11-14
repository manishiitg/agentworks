package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
	agentevents "github.com/manishiitg/mcpagent/events"
)

// PLAT-746-1004: real local Git and workspace/MCP admission; substitute only
// AI generation, the network push process and the external GitHub HTTP server.
func TestWorkspaceGitDeliveryDraftsDoNotCommitAndPublishingUsesOnlySelectedAccount(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("AUTH_SECRET", "delivery-fixture-secret")
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	t.Setenv("LLM_CONFIG_LOCKED", "false")
	f, _ := newExternalCrewCommandFixture(t)
	repo := filepath.Join(f.docs, externalCommandCrewRoot, "app")
	writeFile(t, filepath.Join(repo, "a.txt"), "first\n")
	gitRun(t, repo, "init", "-q", "-b", "main")
	gitRun(t, repo, "add", ".")
	gitRun(t, repo, "commit", "-q", "-m", "baseline")
	gitRun(t, repo, "checkout", "-q", "-b", "feature")
	gitRun(t, repo, "remote", "add", "origin", "git@github.com:example/project.git")
	writeFile(t, filepath.Join(repo, "a.txt"), "first\nchange\n")
	writeFile(t, filepath.Join(repo, "new.txt"), "new file evidence\n")
	const session = "delivery-owner-chat"
	const key = "team:chat:root-owner"
	f.write(t, externalCommandCrewSettings+"/product.json", `{"schema_version":1,"product":"work","id":"team","title":"Team","main_chat_key":"`+key+`"}`)
	const connection = "11111111111111111111111111111111"
	f.write(t, externalCommandCrewSettings+"/workflow.json", `{"id":"team","created_by":"owner","capabilities":{"github_connection_id":"`+connection+`"}}`)
	if err := writeGitHubRecord("owner", githubAccountRecord{ID: connection, Login: "selected-account", SecretName: "FIXTURE_SELECTED"}); err != nil {
		t.Fatal(err)
	}
	oldToken, oldBase, oldDraft, oldRun := githubTokenValue, githubAPIBase, workspaceGitDraftTextForTest, workspaceGitDeliveryRunForTest
	t.Cleanup(func() {
		githubTokenValue = oldToken
		githubAPIBase = oldBase
		workspaceGitDraftTextForTest = oldDraft
		workspaceGitDeliveryRunForTest = oldRun
	})
	githubTokenValue = func(rec *githubAccountRecord) string {
		if rec.ID != connection {
			t.Fatal("borrowed another connection")
		}
		return "fixture-selected-token"
	}
	record := ProductConversationRecord{ProfileID: crewProfileID, ConversationKey: key, WorkspacePath: externalCommandCrewRoot, SessionID: session, Provider: "codex-cli", ConnectionID: "caller-model-account"}
	if err := defaultProductConversationRegistryStore().writeDocument(t.Context(), productConversationRegistryPath("owner"), productConversationRegistryDocument{Entries: map[string]ProductConversationRecord{productConversationRegistryEntryKey(crewProfileID, key): record}}); err != nil {
		t.Fatal(err)
	}
	journal, err := storeevents.OpenSQLiteEventJournal(filepath.Join(t.TempDir(), "events.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	store := storeevents.NewEventStore(100)
	t.Cleanup(store.Stop)
	store.SetDurableJournal(journal)
	f.api.eventStore = store
	for id, owner := range map[string]string{session: "owner", "foreign-chat": "other"} {
		store.SetSessionOwner(id, owner)
		if err := store.SetSessionPersistenceClass(id, storeevents.SessionPersistenceInteractiveChat); err != nil {
			t.Fatal(err)
		}
		store.AddEvent(id, storeevents.Event{ID: id + "-message", Type: "user_message", Timestamp: time.Now(), Data: agentevents.NewAgentEvent(agentevents.NewUserMessageEvent(1, "Evidence from "+id, "user"))})
	}
	beforeHead, beforeStatus := gitOut(t, repo, "rev-parse", "HEAD"), gitOut(t, repo, "status", "--porcelain")
	generated := 0
	workspaceGitDraftTextForTest = func(_ context.Context, actor string, model *publishedProject, input, system string) (string, error) {
		generated++
		if actor != "owner" || model.ConnectionID != "caller-model-account" {
			t.Fatal("draft borrowed another account")
		}
		if strings.Contains(input, "foreign-chat") {
			t.Fatal("foreign chat reached draft")
		}
		if strings.Contains(system, "pull request") {
			if !strings.Contains(input, "Evidence from "+session) || !strings.Contains(input, "message chosen by person") {
				t.Fatal("PR draft omitted admitted chat/commits")
			}
			return `{"title":"Review local change","body":"Changed local files. Checks are not live verified."}`, nil
		}
		if !strings.Contains(input, "new file evidence") {
			t.Fatal("untracked new file omitted")
		}
		return "Editable draft only", nil
	}
	call := func(user string, op string, extra map[string]any) *httptest.ResponseRecorder {
		body := map[string]any{"workspace_path": externalCommandCrewRoot, "repo": "app", "op": op, "session_id": session}
		for k, v := range extra {
			body[k] = v
		}
		w := httptest.NewRecorder()
		f.api.handleWorkspaceGitAction(w, sharedSecretsRequest(http.MethodPost, "/api/workspace-git", user, body))
		return w
	}
	w := call("owner", "draft_commit", map[string]any{"all": true})
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Editable draft only") {
		t.Fatalf("draft: %d %s", w.Code, w.Body.String())
	}
	if gitOut(t, repo, "rev-parse", "HEAD") != beforeHead || gitOut(t, repo, "status", "--porcelain") != beforeStatus {
		t.Fatal("draft changed files, index or HEAD")
	}
	// The same registered MCP action must resolve the same caller/model and may
	// not gain another person's project or chat through its arguments.
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	mcp := serveExternalMCP(t, f.api, crewWriter("owner"))
	cli := dialExternalMCP(t, ctx, mcp.URL+externalMCPPath)
	initializeExternalMCP(t, ctx, cli)
	result := callRemoteTool(t, ctx, cli, "call_tool", map[string]any{"name": "workspace", "arguments": map[string]any{"action": "git_action", "workspace_id": "team", "git_op": "draft_commit", "repo": "app", "all": true, "session_id": session}})
	requireRemoteSuccess(t, result, "workspace")
	if !strings.Contains(marshalStructured(t, result), "Editable draft only") {
		t.Fatal("MCP draft missing")
	}
	if generated != 2 {
		t.Fatal("a draft made more than one AI request")
	}
	for _, extra := range []map[string]any{{"session_id": "foreign-chat"}, {"workspace_path": "Crew/not-admitted"}} {
		if w = call("owner", "draft_commit", extra); w.Code < 400 {
			t.Fatal("unadmitted draft succeeded")
		}
	}
	if w = call("other", "draft_commit", nil); w.Code != 403 {
		t.Fatalf("shared reader draft: %d %s", w.Code, w.Body.String())
	}
	req := sharedSecretsRequest(http.MethodPost, "/api/workspace-git", "owner", map[string]any{"workspace_path": externalCommandCrewRoot, "repo": "app", "op": "commit", "all": true, "message": "forbidden"})
	req = req.WithContext(context.WithValue(req.Context(), externalFileGuardKey{}, &wf.FolderGuard{}))
	limited := httptest.NewRecorder()
	f.api.handleWorkspaceGitAction(limited, req)
	if limited.Code != 403 {
		t.Fatal("narrow file grant committed whole repo")
	}
	if generated != 2 {
		t.Fatal("a denied caller reached the model")
	}
	if w = call("owner", "commit", map[string]any{"all": true, "message": "message chosen by person"}); w.Code != 200 {
		t.Fatalf("commit: %d %s", w.Code, w.Body.String())
	}
	head := gitOut(t, repo, "rev-parse", "HEAD")
	if head == beforeHead || gitOut(t, repo, "log", "-1", "--format=%s") != "message chosen by person" {
		t.Fatal("explicit commit lost edited message")
	}
	pushes, creates := 0, 0
	remoteHead := head
	workspaceGitDeliveryRunForTest = func(cmd *exec.Cmd) error {
		pushes++
		argv := strings.Join(cmd.Args, " ")
		if strings.Contains(argv, "fixture-selected-token") || strings.Contains(argv, "Authorization:") || strings.Contains(argv, "--force") || !strings.Contains(argv, "https://github.com/example/project.git "+head+":refs/heads/feature") {
			t.Fatalf("unsafe push arguments: %v", cmd.Args)
		}
		want := "GIT_CONFIG_VALUE_0=Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte("x-access-token:fixture-selected-token"))
		if !containsString(cmd.Env, want) {
			t.Fatal("selected credential not confined to child env")
		}
		return nil
	}
	for _, key := range []string{"url.https://example.invalid/.insteadOf", "http.sslVerify", "credential.helper", "remote.origin.receivepack"} {
		gitRun(t, repo, "config", key, "fixture")
		if w = call("owner", "push", nil); w.Code != 409 {
			t.Fatalf("network override accepted: %s", key)
		}
		gitRun(t, repo, "config", "--unset", key)
	}
	if pushes != 0 {
		t.Fatal("custom network settings reached push")
	}
	if w = call("owner", "push", nil); w.Code != 200 {
		t.Fatalf("push: %d %s", w.Code, w.Body.String())
	}
	if pushes != 1 || gitOut(t, repo, "rev-parse", "origin/feature") != head {
		t.Fatal("push tracking did not refresh")
	}
	github := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture-selected-token" {
			t.Fatal("wrong GitHub API account")
		}
		switch {
		case r.URL.Path == "/repos/example/project":
			json.NewEncoder(w).Encode(map[string]string{"default_branch": "main"})
		case r.URL.Path == "/repos/example/project/git/ref/heads/feature":
			json.NewEncoder(w).Encode(map[string]any{"object": map[string]string{"sha": remoteHead}})
		case r.URL.Path == "/repos/example/project/pulls" && r.Method == http.MethodPost:
			creates++
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			if body["title"] != "Reviewed title" {
				t.Fatal("PR ignored person review")
			}
			json.NewEncoder(w).Encode(map[string]any{"number": 42, "html_url": "https://untrusted.example/ignored"})
		default:
			t.Fatalf("unexpected API path %s", r.URL.Path)
		}
	}))
	t.Cleanup(github.Close)
	githubAPIBase = github.URL
	if w = call("owner", "draft_pr", nil); w.Code != 200 || !strings.Contains(w.Body.String(), "Review local change") {
		t.Fatalf("PR draft: %d %s", w.Code, w.Body.String())
	}
	if creates != 0 || pushes != 1 || generated != 3 {
		t.Fatal("draft PR mutated repo or called AI twice")
	}
	body := map[string]any{"title": "Reviewed title", "body": "Reviewed body", "base": "main", "expected_head": beforeHead}
	if w = call("owner", "open_pr", body); w.Code != 409 {
		t.Fatal("stale review opened PR")
	}
	body["expected_head"] = head
	if w = call("owner", "open_pr", body); w.Code != 200 || !strings.Contains(w.Body.String(), "https://github.com/example/project/pull/42") {
		t.Fatalf("PR open: %d %s", w.Code, w.Body.String())
	}
	remoteHead = beforeHead
	if w = call("owner", "open_pr", body); w.Code != 409 || creates != 1 {
		t.Fatal("un-pushed current head opened a second PR")
	}
	if pushes != 1 {
		t.Fatal("opening PR silently pushed")
	}
}

// Bound Git output before model invocation, even for a huge repository.
func TestWorkspaceGitDraftReadIsByteBounded(t *testing.T) {
	_, repo := gitTestEnv(t)
	writeFile(t, filepath.Join(repo, "a.txt"), strings.Repeat("line with text\n", 100000))
	raw, clipped, err := workspaceGitDraftRead(t.Context(), repo, 1024, "diff", "--no-ext-diff", "--no-textconv")
	if err != nil || !clipped || len(raw) > 1024 {
		t.Fatalf("unbounded draft: len=%d clipped=%t err=%v", len(raw), clipped, err)
	}
}
