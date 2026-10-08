package workflowfiles

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func testEditor(t *testing.T) (*Editor, string, string) {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "files")
	state := filepath.Join(base, "state")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	editor, err := OpenEditor(root, state)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { editor.Close() })
	return editor, root, state
}

func TestEditorRejectsLockStateInsideRootThroughParentSymlink(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "files")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(base, "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	t.Setenv("WORKSPACE_FILE_STATE_DIR", filepath.Join(alias, "locks"))
	if editor, err := OpenEditor(root, filepath.Join(base, "receipts")); err == nil {
		editor.Close()
		t.Fatal("lock state resolved inside exposed files")
	}
}
func TestEditorGuardsRevisionsAndDurableRetries(t *testing.T) {
	editor, root, state := testEditor(t)
	ctx := context.Background()
	guard := &FolderGuard{WritePaths: []string{"docs"}, ReadOnlyPaths: []string{"docs/locked"}, BlockedPaths: []string{"docs/private"}}
	request := WriteRequest{Root: ".", Path: "docs/readme.md", Content: "hello", ExpectedRevision: MissingRevision, RequestID: "first", Actor: "owner", Guard: guard}
	receipt, err := editor.Write(ctx, request)
	if err != nil || !receipt.Applied {
		t.Fatalf("write: %+v %v", receipt, err)
	}
	read, err := editor.Read(".", request.Path, guard)
	if err != nil || read.Content != "hello" || read.Revision != receipt.Revision {
		t.Fatalf("read: %+v %v", read, err)
	}
	second, err := OpenEditor(root, state)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	retry, err := second.Write(ctx, request)
	if err != nil || retry != receipt {
		t.Fatalf("restart retry: %+v %v", retry, err)
	}
	changed := request
	changed.Content = "different"
	if _, err := editor.Write(ctx, changed); StatusCode(err) != 409 {
		t.Fatalf("changed request_id: %v", err)
	}
	changed.RequestID = "next"
	if _, err := editor.Write(ctx, changed); StatusCode(err) != 409 {
		t.Fatalf("stale revision: %v", err)
	}
	changed.ExpectedRevision = receipt.Revision
	if _, err := editor.Write(ctx, changed); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"planning/plan.json", "workflow.json", "docs/plan.json", "docs/locked/a.txt", "docs/private/a.txt", "code/main.go", "db/db.sqlite", "docs/.env", "../escape", "/absolute"} {
		blocked := request
		blocked.Path = p
		blocked.RequestID = p
		if _, err := editor.Write(ctx, blocked); err == nil {
			t.Fatalf("allowed protected path %q", p)
		}
	}
	// Denied retries stay denied even after a successful receipt exists.
	request.Guard = &FolderGuard{}
	if _, err := editor.Write(ctx, request); StatusCode(err) != 403 {
		t.Fatalf("broadened retry: %v", err)
	}
	if _, err := OpenEditor(root, filepath.Join(root, "public-state")); err == nil {
		t.Fatal("state inside exposed root")
	}
}
func TestEditorCrossProcessLockAndPreparedReceiptRecovery(t *testing.T) {
	editor, root, state := testEditor(t)
	other, err := OpenEditor(root, filepath.Join(filepath.Dir(state), "different-receipts"))
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	var wait sync.WaitGroup
	statuses := make(chan int, 2)
	for i, e := range []*Editor{editor, other} {
		wait.Add(1)
		go func(i int, e *Editor) {
			defer wait.Done()
			_, err := e.Write(context.Background(), WriteRequest{Root: ".", Path: "race.txt", Content: string(rune('a' + i)), ExpectedRevision: MissingRevision, RequestID: string(rune('a' + i)), Actor: "owner"})
			if err == nil {
				statuses <- 200
			} else {
				statuses <- StatusCode(err)
			}
		}(i, e)
	}
	wait.Wait()
	close(statuses)
	successes, conflicts := 0, 0
	for status := range statuses {
		if status == 200 {
			successes++
		}
		if status == 409 {
			conflicts++
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes %d conflicts %d", successes, conflicts)
	}
	request := WriteRequest{Root: ".", Path: "recover.txt", Content: "applied", ExpectedRevision: MissingRevision, RequestID: "recover", Actor: "owner"}
	receipt, err := editor.Write(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	key := Revision([]byte("owner\x00.\x00recover")) + ".json"
	data, err := os.ReadFile(filepath.Join(state, key))
	if err != nil {
		t.Fatal(err)
	}
	var record editRecord
	if err = json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	record.Status = "prepared"
	record.Receipt.Applied = false
	data, _ = json.Marshal(record)
	if err = os.WriteFile(filepath.Join(state, key), data, 0600); err != nil {
		t.Fatal(err)
	}
	recovered, err := editor.Write(context.Background(), request)
	if err != nil || recovered != receipt {
		t.Fatalf("recover applied receipt: %+v %v", recovered, err)
	}
}
func TestEditorRejectsSymlinksAndBinaryAndBounds(t *testing.T) {
	editor, root, _ := testEditor(t)
	os.Mkdir(filepath.Join(root, "docs"), 0700)
	os.Mkdir(filepath.Join(root, "planning"), 0700)
	os.WriteFile(filepath.Join(root, "planning", "plan.json"), []byte("private"), 0600)
	if err := os.Symlink("../planning", filepath.Join(root, "docs", "alias")); err != nil {
		t.Fatal(err)
	}
	if _, err := editor.Read(".", "docs/alias/plan.json", nil); StatusCode(err) != 403 {
		t.Fatalf("symlink read: %v", err)
	}
	if _, err := editor.Write(context.Background(), WriteRequest{Root: ".", Path: "docs/alias/new.md", Content: "bypass", ExpectedRevision: MissingRevision, RequestID: "link", Actor: "owner"}); StatusCode(err) != 403 {
		t.Fatalf("symlink write: %v", err)
	}
	os.WriteFile(filepath.Join(root, "binary"), []byte{0, 1}, 0600)
	if _, err := editor.Read(".", "binary", nil); StatusCode(err) != 400 {
		t.Fatalf("binary: %v", err)
	}
	if _, err := editor.Write(context.Background(), WriteRequest{Root: ".", Path: "big", Content: string(make([]byte, MaxFileBytes+1)), ExpectedRevision: MissingRevision, RequestID: "big", Actor: "owner"}); StatusCode(err) != 413 {
		t.Fatalf("size: %v", err)
	}
}
