package workspace

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeDocuments is a minimal workspace documents API: GET/PUT/DELETE a file
// and PATCH .../diff (with dry_run). A patch containing FAIL_DRY fails every
// time; FAIL_APPLY fails only on the real write. Applying appends a marker.
type fakeDocuments struct {
	mu      sync.Mutex
	files   map[string]string
	patches []string // "dry:<path>" / "write:<path>" in call order
}

func newFakeDocuments(t *testing.T, files map[string]string) (*fakeDocuments, *httptest.Server) {
	f := &fakeDocuments{files: files}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		path := strings.TrimPrefix(r.URL.Path, "/api/documents/")
		reply := func(ok bool, data interface{}, errText string) {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": ok, "data": data, "error": errText})
		}
		switch {
		case r.Method == http.MethodPatch && strings.HasSuffix(path, "/diff"):
			path = strings.TrimSuffix(path, "/diff")
			var body struct {
				Diff   string `json:"diff"`
				DryRun bool   `json:"dry_run"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.DryRun {
				f.patches = append(f.patches, "dry:"+path)
			} else {
				f.patches = append(f.patches, "write:"+path)
			}
			if strings.Contains(body.Diff, "FAIL_DRY") || (!body.DryRun && strings.Contains(body.Diff, "FAIL_APPLY")) {
				reply(false, nil, "Failed to apply diff patch: hunk 1 not found")
				return
			}
			if !body.DryRun {
				f.files[path] += "#patched\n"
			}
			reply(true, map[string]interface{}{"applied": !body.DryRun}, "")
		case r.Method == http.MethodGet:
			content, ok := f.files[path]
			if !ok {
				reply(false, nil, "File does not exist")
				return
			}
			reply(true, map[string]interface{}{"filepath": path, "content": content}, "")
		case r.Method == http.MethodPut:
			var body struct {
				Content string `json:"content"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.files[path] = body.Content
			reply(true, map[string]interface{}{"filepath": path}, "")
		case r.Method == http.MethodDelete:
			delete(f.files, path)
			reply(true, map[string]interface{}{"filepath": path}, "")
		default:
			http.Error(w, "unexpected", http.StatusBadRequest)
		}
	}))
	t.Cleanup(server.Close)
	return f, server
}

func threeFilePatch(extra string) string {
	return "*** Begin Patch\n" +
		"*** Update File: Workflow/wf/code/shared/lib.py\n@@ def a(\n-    return 1\n+    return 2\n" +
		"*** Update File: Workflow/wf/code/step/main.py\n@@\n-from shared.lib import a\n+from shared.lib import a, b" + extra + "\n" +
		"*** Add File: Workflow/wf/code/shared/ids.py\n+ASSETS = 1\n" +
		"*** End Patch\n"
}

func TestMultiFilePatchChecksEveryFileThenWritesAll(t *testing.T) {
	docs, server := newFakeDocuments(t, map[string]string{
		"Workflow/wf/code/shared/lib.py": "lib\n",
		"Workflow/wf/code/step/main.py":  "main\n",
	})
	client := NewClient(server.URL, WithFolderGuard(&FolderGuardConfig{Enabled: true, ReadPaths: []string{"Workflow/wf"}, WritePaths: []string{"Workflow/wf"}}))
	result, err := client.DiffPatchWorkspaceFile(context.Background(), DiffPatchWorkspaceFileParams{Diff: threeFilePatch("")})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"dry:Workflow/wf/code/shared/lib.py", "dry:Workflow/wf/code/step/main.py", "dry:Workflow/wf/code/shared/ids.py",
		"write:Workflow/wf/code/shared/lib.py", "write:Workflow/wf/code/step/main.py", "write:Workflow/wf/code/shared/ids.py"}
	if strings.Join(docs.patches, ",") != strings.Join(want, ",") {
		t.Fatalf("calls = %v, want every dry run before any write", docs.patches)
	}
	if !strings.Contains(string(result.Data), `"created":true`) || !strings.Contains(string(result.Data), "ids.py") {
		t.Fatalf("result = %s", result.Data)
	}
}

func TestMultiFilePatchWritesNothingWhenAnyFileFailsItsCheck(t *testing.T) {
	docs, server := newFakeDocuments(t, map[string]string{
		"Workflow/wf/code/shared/lib.py": "lib\n",
		"Workflow/wf/code/step/main.py":  "main\n",
	})
	client := NewClient(server.URL)
	_, err := client.DiffPatchWorkspaceFile(context.Background(), DiffPatchWorkspaceFileParams{Diff: threeFilePatch(" # FAIL_DRY")})
	if err == nil || !strings.Contains(err.Error(), "main.py") || !strings.Contains(err.Error(), "nothing was changed") {
		t.Fatalf("err = %v, want the failing file named and nothing changed", err)
	}
	for _, call := range docs.patches {
		if strings.HasPrefix(call, "write:") {
			t.Fatalf("a file was written after a failed check: %v", docs.patches)
		}
	}
	if docs.files["Workflow/wf/code/shared/lib.py"] != "lib\n" {
		t.Fatalf("lib.py changed: %q", docs.files["Workflow/wf/code/shared/lib.py"])
	}
}

func TestMultiFilePatchRestoresFilesWhenALaterWriteFails(t *testing.T) {
	docs, server := newFakeDocuments(t, map[string]string{
		"Workflow/wf/code/shared/lib.py": "lib\n",
		"Workflow/wf/code/step/main.py":  "main\n",
	})
	client := NewClient(server.URL)
	// main.py passes its check but fails its write, after lib.py was written.
	_, err := client.DiffPatchWorkspaceFile(context.Background(), DiffPatchWorkspaceFileParams{Diff: threeFilePatch(" # FAIL_APPLY")})
	if err == nil || !strings.Contains(err.Error(), "restored 1") {
		t.Fatalf("err = %v, want the written file restored", err)
	}
	if docs.files["Workflow/wf/code/shared/lib.py"] != "lib\n" || docs.files["Workflow/wf/code/step/main.py"] != "main\n" {
		t.Fatalf("files not restored: %v", docs.files)
	}
	if _, created := docs.files["Workflow/wf/code/shared/ids.py"]; created {
		t.Fatal("the new file must not exist after a failed patch")
	}
}

func TestMultiFilePatchRefusesAPathOutsideTheGuardBeforeAnyRequest(t *testing.T) {
	docs, server := newFakeDocuments(t, map[string]string{"Workflow/wf/code/shared/lib.py": "lib\n"})
	client := NewClient(server.URL, WithFolderGuard(&FolderGuardConfig{Enabled: true, ReadPaths: []string{"Workflow/wf"}, WritePaths: []string{"Workflow/wf"}}))
	patch := "*** Begin Patch\n*** Update File: Workflow/wf/code/shared/lib.py\n-lib\n+x\n*** Update File: Workflow/other/secrets.py\n-a\n+b\n*** End Patch\n"
	_, err := client.DiffPatchWorkspaceFile(context.Background(), DiffPatchWorkspaceFileParams{Diff: patch})
	if err == nil || !strings.Contains(err.Error(), "Workflow/other/secrets.py") {
		t.Fatalf("err = %v, want the out-of-guard file refused", err)
	}
	if len(docs.patches) != 0 {
		t.Fatalf("requests were made before the guard refused: %v", docs.patches)
	}
}

func TestDiffPatchTargetPathsCoversEveryNamedFile(t *testing.T) {
	multi := map[string]interface{}{"filepath": "Workflow/wf/a.py", "diff": threeFilePatch("")}
	got := DiffPatchTargetPaths(multi)
	if len(got) != 4 || got[0] != "Workflow/wf/a.py" || got[3] != "Workflow/wf/code/shared/ids.py" {
		t.Fatalf("multi targets = %v", got)
	}
	single := map[string]interface{}{"filepath": "Workflow/wf/a.py", "diff": "*** Begin Patch\n*** Update File: x.py\n-a\n+b\n*** End Patch\n"}
	if got := DiffPatchTargetPaths(single); len(got) != 1 || got[0] != "Workflow/wf/a.py" {
		t.Fatalf("single with filepath targets = %v (filepath is authoritative)", got)
	}
	noPath := map[string]interface{}{"diff": "*** Begin Patch\n*** Update File: Workflow/wf/x.py\n-a\n+b\n*** End Patch\n"}
	if got := DiffPatchTargetPaths(noPath); len(got) != 1 || got[0] != "Workflow/wf/x.py" {
		t.Fatalf("single without filepath targets = %v", got)
	}
	if _, err := SplitApplyPatch("*** Begin Patch\n*** Delete File: a.py\n*** End Patch\n"); err == nil {
		t.Fatal("Delete File must be refused")
	}
}
