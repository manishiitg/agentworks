package localfiles

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

func patchFixture(t *testing.T) (*Executor, string) {
	t.Helper()
	root := t.TempDir()
	e, err := Open("laptop", []Grant{{Resource: Resource{ID: "project", Writable: true, Guard: wf.FolderGuard{ReadPaths: []string{"."}, WritePaths: []string{"."}, ReadOnlyPaths: []string{"locked"}}}, Root: root, State: filepath.Join(t.TempDir(), "state")}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Close)
	if err := os.WriteFile(filepath.Join(root, "one.txt"), []byte("old\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return e, root
}
func patchRequest(path, diff, id string) Request {
	return Request{Operation: "patch", ResourceID: "project", Path: path, Content: diff, RequestID: id, Identity: wf.EditIdentity{UserID: "owner", Source: "server_local_executor"}}
}
func TestLocalPatchReusesUnifiedAndMultiFileFormats(t *testing.T) {
	e, root := patchFixture(t)
	got := e.Execute(t.Context(), patchRequest(filepath.Join(root, "one.txt"), "--- a/one.txt\n+++ b/one.txt\n@@ -1 +1 @@\n-old\n+new\n", "unified"))
	if got.Status != 200 || len(got.Patches) != 1 || got.Patches[0].Identity.UserID != "owner" {
		t.Fatalf("unified patch %+v", got)
	}
	diff := "*** Begin Patch\n*** Update File: one.txt\n@@\n-new\n+updated\n*** Add File: sub/two.txt\n+created\n*** End Patch\n"
	got = e.Execute(t.Context(), patchRequest("", diff, "multi"))
	if got.Status != 200 || len(got.Patches) != 2 {
		t.Fatalf("multi patch %+v", got)
	}
	for file, want := range map[string]string{"one.txt": "updated\n", "sub/two.txt": "created\n"} {
		data, err := os.ReadFile(filepath.Join(root, file))
		if err != nil || string(data) != want {
			t.Fatalf("%s: %q %v", file, data, err)
		}
	}
}
func TestLocalPatchValidatesAllFilesBeforeWriting(t *testing.T) {
	for _, bad := range []string{"*** Update File: one.txt\n@@\n-missing context\n+bad\n", "*** Add File: ../escape\n+bad\n", "*** Add File: planning/plan.json\n+{}\n", "*** Add File: locked/two.txt\n+bad\n", "*** Add File: one.txt\n+bad\n"} {
		t.Run(strings.Split(bad, "\n")[0], func(t *testing.T) {
			e, root := patchFixture(t)
			diff := "*** Begin Patch\n*** Add File: new.txt\n+should not appear\n" + bad + "*** End Patch\n"
			got := e.Execute(t.Context(), patchRequest("", diff, "reject"))
			if got.Status == 200 {
				t.Fatalf("invalid patch admitted %+v", got)
			}
			if _, err := os.Stat(filepath.Join(root, "new.txt")); !os.IsNotExist(err) {
				t.Fatalf("partial write: %v", err)
			}
			data, _ := os.ReadFile(filepath.Join(root, "one.txt"))
			if string(data) != "old\n" {
				t.Fatalf("original modified %q", data)
			}
		})
	}
}
func TestLocalPatchRejectsOutsideAbsolutePathAndReadOnlyFolder(t *testing.T) {
	e, _ := patchFixture(t)
	diff := "@@ -1 +1 @@\n-old\n+new\n"
	if got := e.Execute(t.Context(), patchRequest(filepath.Join(t.TempDir(), "outside.txt"), diff, "outside")); got.Status != 403 {
		t.Fatalf("outside patch %+v", got)
	}
	g := e.grants["project"]
	g.Writable = false
	e.grants["project"] = g
	if got := e.Execute(t.Context(), patchRequest("one.txt", diff, "readonly")); got.Status != 403 {
		t.Fatalf("read-only patch %+v", got)
	}
}
