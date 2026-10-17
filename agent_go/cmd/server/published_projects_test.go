package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPublishedProjectKeepsSourceAndHistoryPrivate(t *testing.T) {
	_, _ = newCodePrivacyFixture(t)
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
