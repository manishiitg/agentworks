package handlers

import (
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
	"github.com/spf13/viper"
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

func TestVersionRestoreWaitsForGuardedWorkflowWrites(t *testing.T) {
	docs := t.TempDir()
	root := filepath.Join(docs, "Workflow", "one")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "file.txt")
	if err := os.WriteFile(file, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", docs, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false"}, args...)...)
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, output)
		}
		return strings.TrimSpace(string(output))
	}
	git("init", "-q")
	git("add", "Workflow/one/file.txt")
	git("commit", "-qm", "original")
	commit := git("rev-parse", "HEAD")
	if err := os.WriteFile(file, []byte("current"), 0600); err != nil {
		t.Fatal(err)
	}
	previous := viper.GetString("docs-dir")
	viper.Set("docs-dir", docs)
	t.Cleanup(func() { viper.Set("docs-dir", previous) })
	t.Setenv("WORKSPACE_FILE_STATE_DIR", filepath.Join(t.TempDir(), "locks"))
	unlock, err := wf.LockWorkspace(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if unlock != nil {
			unlock()
		}
	}()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest("POST", "/", strings.NewReader(`{"commit_hash":"`+commit+`"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "filepath", Value: "/Workflow/one/file.txt"}}
	done := make(chan struct{})
	go func() { RestoreFileVersion(c); close(done) }()
	select {
	case <-done:
		t.Fatal("restore bypassed the guarded write lock")
	case <-time.After(100 * time.Millisecond):
	}
	if data, _ := os.ReadFile(file); string(data) != "current" {
		t.Fatalf("restore replaced files while locked: %s", data)
	}
	unlock()
	unlock = nil
	select {
	case <-done:
		if recorder.Code != 200 {
			t.Fatalf("restore: %d %s", recorder.Code, recorder.Body.String())
		}
		if data, _ := os.ReadFile(file); string(data) != "original" {
			t.Fatalf("restore content: %s", data)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("restore stayed blocked")
	}
}
