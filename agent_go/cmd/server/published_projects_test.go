package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPublishedProjectKeepsSourceAndHistoryPrivate(t *testing.T) {
	api, _ := newCodePrivacyFixture(t)
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	registry := withProjectOwnerRegistry(t)
	if err := registry.Register(projectOwnerRecord{Product: "code", Folder: "app-c0de0001", OwnerID: "owner", ProjectID: "c0de0001-0000"}); err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner", Username: "owner"})
	record := &publishedProject{Root: codePrivacyOwnerRoot, WorkspaceID: "c0de0001-0000", ProfileID: "code", OwnerID: "owner", ProjectID: "stable-project", Revision: "rev-1"}
	if err := savePublishedProject(record); err != nil {
		t.Fatal(err)
	}
	terminal := api.codeShellFolderGuard(ctx, "owner", record.Root)
	for _, write := range terminal.WritePaths {
		if strings.TrimSuffix(write, "/") == record.Root || strings.HasPrefix(record.sourceRoot(), strings.TrimSuffix(write, "/")+"/") {
			t.Fatal("Terminal can write published source:", write)
		}
	}
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
	if !publishedBuilderHandoff(&QueryRequest{}, false) || publishedBuilderHandoff(&QueryRequest{}, true) || publishedBuilderHandoff(&QueryRequest{TriggeredBy: "function"}, false) {
		t.Fatal("private handoff crossed interactive Builder boundary")
	}
}

// Adding an agent must not freeze or overwrite the existing Crew. Its source
// and private context stay separate from Main, while republish keeps the ID.
func TestPublishedAgentKeepsDestinationEditableAndPinsItsSource(t *testing.T) {
	_, _ = newCodePrivacyFixture(t)
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	registry := withProjectOwnerRegistry(t)
	if err := registry.Register(projectOwnerRecord{Product: "code", Folder: "app-c0de0001", OwnerID: "owner", ProjectID: "c0de0001-0000"}); err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: "owner", Username: "owner"})
	p := &publishedProject{Root: codePrivacyOwnerRoot, WorkspaceID: "c0de0001-0000", ProfileID: "code", OwnerID: "owner", PublisherID: "owner", AgentID: "37b46a91-e76d-4b5c-8e05-ad78acbc1fd3", Name: "Invoices", ProjectID: "source", Revision: "revision-one", SourcePaths: []string{"code/run.py", "assets/logo.png"}}
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
	loaded, err := readPublishedAgent(ctx, p.Root, p.AgentID)
	if err != nil || loaded.Revision != next.Revision || loaded.AgentID != p.AgentID {
		t.Fatalf("republish lost stable identity: %+v %v", loaded, err)
	}
}
