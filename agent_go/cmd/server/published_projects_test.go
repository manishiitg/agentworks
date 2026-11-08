package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	workspaceclient "github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	workspacehandlers "github.com/manishiitg/coding-agent-loop/workspace/handlers"
	"github.com/manishiitg/coding-agent-loop/workspace/publication"
	"github.com/spf13/viper"
)

// Website history uses llmtypes human/ai roles, unlike native CLI archives.
// Dropping ai silently removed the answer and proven result from handoffs.
func TestPublicationHandoffPreservesStoredConversationRoles(t *testing.T) {
	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(previous) })
	raw := []byte(`{"conversation_history":[
	 {"Role":"system","Parts":[{"Text":"SYSTEM_CONTEXT_ONLY"}]},
	 {"Role":"human","Parts":[{"Text":"Remember reference CUSTOMER_REF_ORANGE."}]},
	 {"Role":"ai","Parts":[{"Text":"Saved CUSTOMER_REF_ORANGE; report completed at reports/result.html."}]},
	 {"role":"tool","content":"TOOL_RECORD_ONLY"},
	 {"role":"function","content":"FUNCTION_RECORD_ONLY"},
	 {"role":"unsupported","content":"UNKNOWN_RECORD_ONLY"}
	]}`)
	handoff := publication.HandoffTranscript(raw)
	for _, wanted := range []string{"user: Remember reference CUSTOMER_REF_ORANGE.", "assistant: Saved CUSTOMER_REF_ORANGE; report completed at reports/result.html."} {
		if !strings.Contains(handoff, wanted) {
			t.Fatalf("saved conversation lost context %q: %s", wanted, handoff)
		}
	}
	for _, omitted := range []string{"SYSTEM_CONTEXT_ONLY", "TOOL_RECORD_ONLY", "FUNCTION_RECORD_ONLY", "UNKNOWN_RECORD_ONLY"} {
		if strings.Contains(handoff, omitted) || strings.Contains(logs.String(), omitted) {
			t.Fatalf("non-conversation record was copied: %s", omitted)
		}
	}
	if !strings.Contains(logs.String(), "messages=2 omitted_system=1 omitted_tools=2 omitted_unknown=1") {
		t.Fatalf("omitted record categories were not recorded: %s", logs.String())
	}
}

func TestPublishedProjectKeepsSourceAndHistoryPrivate(t *testing.T) {
	// Linux's launcher gives private /tmp scratch access. Hosted source must
	// be outside it so that scratch access cannot mask a broken write guard.
	if runtime.GOOS == "linux" && os.Getenv("AGENTWORKS_LANDLOCK_RUNNER") != "" {
		root := os.Getenv("AGENTWORKS_LINUX_TEST_ROOT")
		if !filepath.IsAbs(root) || root == "/tmp" || strings.HasPrefix(root, "/tmp/") {
			t.Fatal("native source-write check needs the verifier's hosted fixture root")
		}
		t.Setenv("TMPDIR", root)
	}
	api, _ := newCodePrivacyFixture(t)
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	registry := withProjectOwnerRegistry(t)
	withProjectSharing(t, true)
	if err := registry.Register(projectOwnerRecord{Product: "work", Folder: "app-c0de0001", OwnerID: "owner", ProjectID: "c0de0001-0000", Shared: true, Private: true, AudienceConfigured: true}); err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner", Username: "owner"})
	record := &publishedProject{Root: codePrivacyOwnerRoot, WorkspaceID: "c0de0001-0000", ProfileID: "work", OwnerID: "owner", ProjectID: "stable-project", Revision: "rev-1"}
	if err := savePublishedProject(record); err != nil {
		t.Fatal(err)
	}
	terminal := api.codeShellFolderGuard(ctx, "owner", record.Root)
	// Source revisions live outside the mutable Crew folder. Check both
	// logical and absolute grants against that actual immutable location.
	source := filepath.Join(os.Getenv("WORKSPACE_DOCS_PATH"), filepath.FromSlash(record.sourceRoot()))
	for _, write := range terminal.WritePaths {
		grant := filepath.FromSlash(write)
		if !filepath.IsAbs(grant) {
			grant = filepath.Join(os.Getenv("WORKSPACE_DOCS_PATH"), grant)
		}
		relative, err := filepath.Rel(grant, source)
		if err != nil || relative == "." || relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			t.Fatal("Terminal can write published source:", write)
		}
	}
	t.Run("native shell refuses published source write", func(t *testing.T) {
		if runtime.GOOS != "linux" || os.Getenv("AGENTWORKS_LANDLOCK_RUNNER") == "" {
			t.Skip("actual kernel refusal requires the built Linux runner")
		}
		t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
		sourceFile := filepath.Join(source, "code", "frozen.txt")
		if err := os.MkdirAll(filepath.Dir(sourceFile), 0700); err != nil {
			t.Fatal(err)
		}
		const original = "PUBLICATION_SOURCE_ORIGINAL"
		if err := os.WriteFile(sourceFile, []byte(original), 0600); err != nil {
			t.Fatal(err)
		}
		docs := os.Getenv("WORKSPACE_DOCS_PATH")
		previousDocs := viper.Get("docs-dir")
		viper.Set("docs-dir", docs)
		t.Cleanup(func() { viper.Set("docs-dir", previousDocs) })
		if err := os.MkdirAll(filepath.Join(docs, record.Root, "outputs"), 0700); err != nil {
			t.Fatal(err)
		}
		router := gin.New()
		router.POST("/api/execute", workspacehandlers.ExecuteShellCommand)
		server := httptest.NewServer(router)
		defer server.Close()
		client := workspaceclient.NewClient(server.URL, workspaceclient.WithUserID("owner"))
		runCtx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		defer cancel()
		seconds := 15
		quotedSource := "'" + strings.ReplaceAll(sourceFile, "'", "'\"'\"'") + "'"
		out, err := client.ExecuteShellCommand(runCtx, workspaceclient.ExecuteShellCommandParams{
			Command:          "if printf changed > " + quotedSource + "; then exit 41; fi && cat " + quotedSource + " && printf allowed > outputs/publication-guard-proof.txt",
			WorkingDirectory: record.Root, Timeout: &seconds, FolderGuard: terminal,
			ExtraEnv: map[string]string{"PUBLICATION_SOURCE_TEST": sourceFile},
		})
		if err != nil || out.ExitCode != 0 || !strings.Contains(out.Stdout, original) || !strings.Contains(strings.ToLower(out.Stderr), "permission denied") {
			t.Fatalf("native source-write refusal or allowed control failed: exit=%d err=%v stdout=%q stderr=%q", out.ExitCode, err, out.Stdout, out.Stderr)
		}
		if got := mustRead(t, sourceFile); got != original {
			t.Fatal("native shell changed the frozen source")
		}
		if got := mustRead(t, filepath.Join(docs, record.Root, "outputs", "publication-guard-proof.txt")); got != "allowed" {
			t.Fatal("native shell did not complete the allowed write")
		}
		t.Log("native published-source write refused; source read and runtime output write completed")
	})
	for _, rel := range []string{"code/main.py", "functions.json", "skills/prepare/SKILL.md", "PROJECT_INSTRUCTIONS.md", ".published/rev-1/source/code/main.py"} {
		if publishedWriteRefusal(ctx, record.Root+"/"+rel) == "" {
			t.Fatalf("source write admitted: %s", rel)
		}
	}
	for _, rel := range []string{"db/runtime.json", "outputs/result.txt", "reports/result.html"} {
		if got := publishedWriteRefusal(ctx, record.Root+"/"+rel); got != "" {
			t.Fatalf("runtime write refused: %s", got)
		}
	}
	if got := publishedReadPath(ctx, record.Root+"/code/main.py"); got != record.sourceRoot()+"/code/main.py" {
		t.Fatalf("source selection: %s", got)
	}
	pinned := context.WithValue(ctx, publicationExecutionKey{}, record)
	next := *record
	next.Revision = "rev-2"
	if err := savePublishedProject(&next); err != nil {
		t.Fatal(err)
	}
	if got := publishedReadPath(pinned, record.Root+"/code/main.py"); got != record.sourceRoot()+"/code/main.py" {
		t.Fatalf("in-flight execution changed source after republish: %s", got)
	}
	if got := publishedReadPath(ctx, record.Root+"/code/main.py"); got != next.sourceRoot()+"/code/main.py" {
		t.Fatalf("new execution did not see republish: %s", got)
	}
	history, err := record.historyDir()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(history, "workspace-docs") || history == record.sourceRoot() {
		t.Fatalf("history stored in product source: %s", history)
	}
	if err = os.MkdirAll(history, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(history, "summary.md"), []byte("PRIVATE_HANDOFF"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(history, "raw.jsonl"), []byte("PRIVATE_RAW_HISTORY"), 0600); err != nil {
		t.Fatal(err)
	}
	// The test's current record is the newer revision; the old archive still
	// belongs to its original receipt rather than becoming a shared file.
	latestHistory, _ := next.historyDir()
	if err := os.MkdirAll(latestHistory, 0700); err != nil {
		t.Fatal(err)
	}
	rawArchive := []byte("PRIVATE_RAW_HISTORY🙂")
	if err := os.WriteFile(filepath.Join(latestHistory, "raw.jsonl"), rawArchive, 0600); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/external/v1/publication-history?workspace_id=work:c0de0001-0000&agent_id=main", nil).WithContext(ctx)
	ownerDownload := httptest.NewRecorder()
	api.handlePublicationHistory(ownerDownload, request)
	if ownerDownload.Code != 200 || !strings.Contains(ownerDownload.Body.String(), "PRIVATE_RAW_HISTORY") {
		t.Fatalf("Builder history download=%d %s", ownerDownload.Code, ownerDownload.Body.String())
	}
	readSaved := func(c context.Context, offset, limit int) *httptest.ResponseRecorder {
		t.Helper()
		response := httptest.NewRecorder()
		api.externalWorkspaceCall(response, httptest.NewRequest(http.MethodPost, "/mcp", nil).WithContext(c), map[string]any{"action": "read_saved_chat", "workspace_id": "work:c0de0001-0000", "agent_id": "main", "offset": offset, "limit": limit})
		return response
	}
	expectedHash := sha256.Sum256(rawArchive)
	var rebuilt []byte
	for offset := 0; offset < len(rawArchive); {
		response := readSaved(ctx, offset, 7)
		var page struct {
			Content  string `json:"content"`
			Encoding string `json:"encoding"`
			Next     int    `json:"next_offset"`
			SHA      string `json:"sha256"`
		}
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &page) != nil || page.Next <= offset || page.SHA != hex.EncodeToString(expectedHash[:]) {
			t.Fatalf("private history page=%d %s", response.Code, response.Body.String())
		}
		chunk := []byte(page.Content)
		if page.Encoding == "base64" {
			chunk, err = base64.StdEncoding.DecodeString(page.Content)
			if err != nil {
				t.Fatal(err)
			}
		}
		rebuilt = append(rebuilt, chunk...)
		offset = page.Next
	}
	if !bytes.Equal(rebuilt, rawArchive) {
		t.Fatal("MCP changed archived bytes across page boundaries")
	}
	narrow := context.WithValue(ctx, UserContextKey, &UserClaims{UserID: "owner", Username: "owner", AccessToken: &accesstokens.Token{Scopes: []string{"crews:read"}, CrewIDs: []string{"c0de0001-0000"}}})
	if response := readSaved(narrow, 0, 7); response.Code < 400 {
		t.Fatal("saved history ignored connection scope")
	}
	if err := registry.SetRunPeople("work", "app-c0de0001", []string{"other"}); err != nil {
		t.Fatal(err)
	}
	other := context.WithValue(ctx, UserContextKey, &UserClaims{UserID: "other", Username: "other"})
	if binding, _, ok := api.resolveExternalWorkspace(other, GetUserFromContext(other), "work:c0de0001-0000"); !ok || binding.OwnedByCaller {
		t.Fatal("history refusal did not exercise a shared Run caller")
	}
	denied := httptest.NewRecorder()
	api.handlePublicationHistory(denied, request.WithContext(other))
	if denied.Code < 400 || strings.Contains(denied.Body.String(), "PRIVATE_RAW_HISTORY") {
		t.Fatal("private imported history reached another caller")
	}
	if response := readSaved(other, 0, 7); response.Code < 400 {
		t.Fatal("Run caller read saved chat over MCP")
	}
	if err := registry.SetRunPeople("work", "app-c0de0001", nil); err != nil {
		t.Fatal(err)
	}
	if response := readSaved(other, 7, 7); response.Code < 400 {
		t.Fatal("revoked Builder continued reading saved chat")
	}
	if !publishedBuilderHandoff(&QueryRequest{}, false) || !publishedBuilderHandoff(&QueryRequest{TriggeredBy: "cron"}, false) ||
		publishedBuilderHandoff(&QueryRequest{}, true) || publishedBuilderHandoff(&QueryRequest{AgentMessageFromOtherPerson: true}, false) {
		t.Fatal("private handoff must follow the owner's own chat, automatic turns included, and never another person")
	}
}

// Adding an agent must not freeze or overwrite the existing Crew. Its source
// and private context stay separate from Main, while republish keeps the ID.
func TestPublishedAgentKeepsDestinationEditableAndPinsItsSource(t *testing.T) {
	api, _ := newCodePrivacyFixture(t)
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	registry := withProjectOwnerRegistry(t)
	if err := registry.Register(projectOwnerRecord{Product: "work", Folder: "app-c0de0001", OwnerID: "owner", ProjectID: "c0de0001-0000", Shared: true, Private: true, AudienceConfigured: true}); err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner", Username: "owner"})
	p := &publishedProject{Root: codePrivacyOwnerRoot, WorkspaceID: "c0de0001-0000", ProfileID: "work", OwnerID: "owner", PublisherID: "owner", AgentID: "37b46a91-e76d-4b5c-8e05-ad78acbc1fd3", Name: "Invoices", ProjectID: "source", Revision: "revision-one", SourcePaths: []string{"code/run.py", "assets/logo.png"}}
	if err := savePublishedProject(p); err != nil {
		t.Fatal(err)
	}
	if main, err := readPublishedProject(ctx, p.Root); err != nil || main != nil {
		t.Fatalf("agent replaced Main: %v %v", main, err)
	}
	if got := publishedWriteRefusal(ctx, p.Root+"/PROJECT_INSTRUCTIONS.md"); got != "" {
		t.Fatal("ordinary Crew editing changed:", got)
	}
	if strings.HasPrefix(p.sourceRoot(), p.Root+"/") {
		t.Fatal("imported source is under the existing writable Crew")
	}
	if publicationAgentID(p.conversationKey()) != p.AgentID {
		t.Fatal("project identity was lost in the private tab binding")
	}
	running := context.WithValue(ctx, publicationExecutionKey{}, p)
	if got := publishedWriteRefusal(running, p.Root+"/code/run.py"); got == "" {
		t.Fatal("published agent source became writable")
	}
	if got := publishedWriteRefusal(running, p.sourceRoot()+"/assets/logo.png"); got == "" {
		t.Fatal("physical source bypassed the publication rule")
	}
	if got := publishedWriteRefusal(running, p.runtimeRoot()+"/reports/result.txt"); got != "" {
		t.Fatal(got)
	}
	if got := publishedReadPath(running, p.Root+"/assets/logo.png"); got != p.sourceRoot()+"/assets/logo.png" {
		t.Fatal("source asset was replaced by runtime assets:", got)
	}
	next := *p
	next.Revision = "revision-two"
	next.Name = "Invoices updated"
	if err := savePublishedProject(&next); err != nil {
		t.Fatal(err)
	}
	if got := publishedReadPath(running, p.Root+"/code/run.py"); got != p.sourceRoot()+"/code/run.py" {
		t.Fatal("running agent changed revision:", got)
	}
	docs := os.Getenv("WORKSPACE_DOCS_PATH")
	sourceFile := filepath.Join(docs, filepath.FromSlash(next.sourceRoot()), "code/run.py")
	if err := os.MkdirAll(filepath.Dir(sourceFile), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sourceFile, []byte("SHARED_AGENT_SOURCE"), 0600); err != nil {
		t.Fatal(err)
	}
	withProjectSharing(t, true)
	if err := registry.SetRunPeople("work", "app-c0de0001", []string{"other"}); err != nil {
		t.Fatal(err)
	}
	other := context.WithValue(ctx, UserContextKey, &UserClaims{UserID: "other", Username: "other"})
	request := httptest.NewRequest(http.MethodPost, "/", nil).WithContext(other)
	args := map[string]any{"crew_id": p.WorkspaceID, "agent_id": p.AgentID, "path": "code/run.py"}
	shared := httptest.NewRecorder()
	api.externalCrewCall(shared, request, "read_crew_file", args)
	if shared.Code != 200 || !strings.Contains(shared.Body.String(), "SHARED_AGENT_SOURCE") {
		t.Fatalf("shared source selector=%d %s", shared.Code, shared.Body.String())
	}
	prefixed := httptest.NewRecorder()
	prefixedArgs := map[string]any{"crew_id": "work:" + p.WorkspaceID, "agent_id": p.AgentID, "path": "code/run.py"}
	api.externalCrewCall(prefixed, request, "read_crew_file", prefixedArgs)
	if prefixed.Code != http.StatusNotFound || !strings.Contains(prefixed.Body.String(), `"code":"not_found"`) || strings.Contains(prefixed.Body.String(), "SHARED_AGENT_SOURCE") {
		t.Fatalf("prefixed Crew identity was admitted or exposed source: HTTP%d", prefixed.Code)
	}
	privateHistory, err := next.historyDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(privateHistory, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"summary.md", "handoff.md"} {
		if err := os.WriteFile(filepath.Join(privateHistory, name), []byte("PRIVATE_BUILDER_HANDOFF"), 0600); err != nil {
			t.Fatal(err)
		}
		for _, tool := range []string{"read_crew_file", "list_crew_files"} {
			private := httptest.NewRecorder()
			api.externalCrewCall(private, request, tool, map[string]any{"crew_id": p.WorkspaceID, "agent_id": p.AgentID, "path": filepath.ToSlash(filepath.Join(privateHistory, name))})
			if private.Code < 400 || strings.Contains(private.Body.String(), "PRIVATE_BUILDER_HANDOFF") {
				t.Fatalf("Run source selector reached private Builder history: %s %d %s", tool, private.Code, private.Body.String())
			}
		}
	}
	// An imported agent still reads shared Crew resources when they were
	// absent from its own import, including a Crew whose Main was published.
	main := *p
	main.AgentID, main.Revision = "", "main-revision"
	main.SourcePaths = []string{"memory/team.md"}
	if err := savePublishedProject(&main); err != nil {
		t.Fatal(err)
	}
	memory := filepath.Join(docs, filepath.FromSlash(main.sourceRoot()), "memory/team.md")
	if err := os.MkdirAll(filepath.Dir(memory), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(memory, []byte("SHARED_CREW_MEMORY"), 0600); err != nil {
		t.Fatal(err)
	}
	args["path"] = "memory/team.md"
	sharedMemory := httptest.NewRecorder()
	api.externalCrewCall(sharedMemory, request, "read_crew_file", args)
	if sharedMemory.Code != 200 || !strings.Contains(sharedMemory.Body.String(), "SHARED_CREW_MEMORY") {
		t.Fatalf("shared Main resource=%d %s", sharedMemory.Code, sharedMemory.Body.String())
	}
	args["path"] = "code/run.py"
	if err := registry.SetRunPeople("work", "app-c0de0001", nil); err != nil {
		t.Fatal(err)
	}
	revoked := httptest.NewRecorder()
	api.externalCrewCall(revoked, request, "read_crew_file", args)
	if revoked.Code < 400 || strings.Contains(revoked.Body.String(), "SHARED_AGENT_SOURCE") {
		t.Fatal("agent source survived Share revocation")
	}

	loaded, err := readPublishedAgent(ctx, p.Root, p.AgentID)
	if err != nil || loaded.Revision != next.Revision || loaded.AgentID != p.AgentID {
		t.Fatalf("republish lost stable identity: %+v %v", loaded, err)
	}
}
