package handlers

import (
	"context"
	"github.com/gin-gonic/gin"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
	"github.com/spf13/viper"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestManagedMutationsSerializeOnlyTheAffectedWorkflow(t *testing.T) {
	base := t.TempDir()
	docs := filepath.Join(base, "docs")
	state := filepath.Join(base, "state")
	for _, name := range []string{"one", "two"} {
		if err := os.MkdirAll(filepath.Join(docs, "Workflow", name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	previous := viper.GetString("docs-dir")
	viper.Set("docs-dir", docs)
	t.Cleanup(func() { viper.Set("docs-dir", previous) })
	t.Setenv("WORKSPACE_FILE_STATE_DIR", filepath.Join(base, "shared-locks"))
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("PUT", "/", nil)
	c.Params = gin.Params{{Key: "filepath", Value: "/Workflow/one/file.txt"}}
	unlock, ok := lockFileMutation(c)
	if !ok {
		t.Fatal("managed lock unavailable")
	}
	defer func() {
		if unlock != nil {
			unlock()
		}
	}()
	e, err := wf.OpenEditor(docs, state)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	request := wf.WriteRequest{Root: "Workflow/two", Path: "file.txt", Content: "second", Actor: "owner", RequestID: "two", ExpectedRevision: wf.MissingRevision}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if _, err := e.Write(ctx, request); err != nil {
		t.Fatalf("unrelated workflow blocked: %v", err)
	}
	request.Root, request.RequestID = "Workflow/one", "one"
	done := make(chan error, 1)
	go func() { _, err := e.Write(t.Context(), request); done <- err }()
	select {
	case err := <-done:
		t.Fatalf("same-workflow guarded edit bypassed managed lock: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	unlock()
	unlock = nil
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("guarded edit stayed blocked")
	}
}
