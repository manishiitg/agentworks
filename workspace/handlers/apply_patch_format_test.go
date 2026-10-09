package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
)

const applyPatchLib = `import os


def open_assets(page):
    page.click("#assets")
    return True


def select_cases(cases):
    return [c for c in cases if c]


def run(page):
    open_assets(page)
    return select_cases([1, 0, 2])
`

func TestApplyPatchFormatUpdatesByContextAndAnchor(t *testing.T) {
	patch := `*** Begin Patch
*** Update File: Workflow/automationtesting/code/shared/rts_pw_lib.py
@@ def open_assets(page):
-    page.click("#assets")
+    page.click("[data-testid=assets]")
     return True
@@ def run(page):
     open_assets(page)
-    return select_cases([1, 0, 2])
+    return select_cases([1, 2])
*** End Patch
`
	got, err := ApplyDiffPatchDirect(applyPatchLib, patch)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(strings.Replace(applyPatchLib, `page.click("#assets")`, `page.click("[data-testid=assets]")`, 1), "select_cases([1, 0, 2])", "select_cases([1, 2])", 1)
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	// Nothing after the edits was lost: the whole tail is intact.
	if !strings.HasSuffix(got, "    return select_cases([1, 2])\n") || !strings.Contains(got, "def select_cases(cases):") {
		t.Fatalf("tail lost:\n%s", got)
	}
}

func TestApplyPatchFormatInsertsAfterAnchorAndAtEndOfFile(t *testing.T) {
	patch := "*** Begin Patch\n*** Update File: lib.py\n@@ import os\n+import sys\n@@\n+\n+def extra():\n+    return 1\n*** End of File\n*** End Patch\n"
	got, err := ApplyDiffPatchDirect(applyPatchLib, patch)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "import os\nimport sys\n") || !strings.HasSuffix(got, "def extra():\n    return 1\n") {
		t.Fatalf("got:\n%s", got)
	}
}

func TestApplyPatchFormatAddsNewFileOnly(t *testing.T) {
	got, err := ApplyDiffPatchDirect("", "*** Begin Patch\n*** Add File: new.py\n+print('hi')\n*** End Patch\n")
	if err != nil || got != "print('hi')\n" {
		t.Fatalf("add file = %q, %v", got, err)
	}
	if _, err := ApplyDiffPatchDirect(applyPatchLib, "*** Begin Patch\n*** Add File: lib.py\n+x\n*** End Patch\n"); err == nil {
		t.Fatal("Add File over an existing file must be refused")
	}
}

func TestApplyPatchFormatRefusesWhatItCannotPlace(t *testing.T) {
	cases := map[string]string{
		"missing context": "*** Begin Patch\n*** Update File: lib.py\n@@\n-    page.click(\"#nope\")\n+    x\n*** End Patch\n",
		"missing anchor":  "*** Begin Patch\n*** Update File: lib.py\n@@ def nowhere():\n-    return True\n+    return False\n*** End Patch\n",
		"two files":       "*** Begin Patch\n*** Update File: a.py\n-import os\n+import re\n*** Update File: b.py\n-x\n+y\n*** End Patch\n",
		"delete":          "*** Begin Patch\n*** Delete File: lib.py\n*** End Patch\n",
	}
	for name, patch := range cases {
		got, err := ApplyDiffPatchDirect(applyPatchLib, patch)
		if err == nil {
			t.Errorf("%s: expected a refusal, got:\n%s", name, got)
		}
	}
}

// The shape of the real server A patch: the @@ anchor is only the start of the
// signature line, and that line is also the first line the hunk replaces.
func TestApplyPatchFormatPrefixAnchorThatIsAlsoTheFirstRemovedLine(t *testing.T) {
	src := "import os\n\n\ndef helper():\n    return 1\n\n\ndef open_assets(page: Page) -> tuple[bool, str]:\n    page.click(\"#assets\")\n    return True, \"\"\n\n\ndef tail():\n    return 2\n"
	patch := "*** Begin Patch\n*** Update File: lib.py\n@@ def open_assets(\n-def open_assets(page: Page) -> tuple[bool, str]:\n-    page.click(\"#assets\")\n+def open_assets(page: Page, *, wait: bool = True) -> tuple[bool, str]:\n+    page.click(\"[data-testid=assets]\")\n     return True, \"\"\n*** End Patch\n"
	got, err := ApplyDiffPatchDirect(src, patch)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "def open_assets(page: Page, *, wait: bool = True)") || strings.Contains(got, "#assets\"") {
		t.Fatalf("hunk not applied:\n%s", got)
	}
	if !strings.HasSuffix(got, "def tail():\n    return 2\n") || !strings.Contains(got, "def helper():") {
		t.Fatalf("code outside the hunk changed:\n%s", got)
	}
}

// "return True" recurs at many indentations in Python. A hunk whose lines
// match only with different indentation must match exactly once, or it is
// refused rather than patching the wrong function.
func TestApplyPatchFormatRefusesAmbiguousIndentationInsensitiveMatch(t *testing.T) {
	src := "def a():\n    if x:\n        return True\n\n\ndef b():\n    if y:\n        return True\n"
	loose := "*** Begin Patch\n*** Update File: lib.py\n@@\n-return True\n+return False\n*** End Patch\n"
	if got, err := ApplyDiffPatchDirect(src, loose); err == nil {
		t.Fatalf("ambiguous loose match was applied:\n%s", got)
	}
	// An anchor narrows it to one place; the loose match after it is unique.
	anchored := "*** Begin Patch\n*** Update File: lib.py\n@@ def b():\n-return True\n+        return False\n*** End Patch\n"
	got, err := ApplyDiffPatchDirect(src, anchored)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "def a():\n    if x:\n        return True\n") || !strings.HasSuffix(got, "    if y:\n        return False\n") {
		t.Fatalf("wrong place patched:\n%s", got)
	}
}

// Identical exact blocks: hunks apply in order, each searching after the
// previous one, so two identical hunks change both occurrences in turn.
func TestApplyPatchFormatRepeatedExactBlocksApplyInOrder(t *testing.T) {
	src := "x = 1\nprint(x)\nx = 1\nprint(x)\n"
	patch := "*** Begin Patch\n*** Update File: a.py\n@@\n-x = 1\n+x = 2\n@@\n-x = 1\n+x = 3\n*** End Patch\n"
	got, err := ApplyDiffPatchDirect(src, patch)
	if err != nil || got != "x = 2\nprint(x)\nx = 3\nprint(x)\n" {
		t.Fatalf("got %q, %v", got, err)
	}
}

// CRLF patches and files, a file without a final newline, and blank context
// lines that lost their leading space.
func TestApplyPatchFormatLineEndingsAndBlankContext(t *testing.T) {
	crlf := "*** Begin Patch\r\n*** Update File: a.py\r\n@@\r\n-a = 1\r\n+a = 2\r\n*** End Patch\r\n"
	if got, err := ApplyDiffPatchDirect("a = 1\r\nb = 2\r\n", crlf); err != nil || got != "a = 2\nb = 2\n" {
		t.Fatalf("crlf: got %q, %v", got, err)
	}
	if got, err := ApplyDiffPatchDirect("a = 1\nb = 2", "*** Begin Patch\n*** Update File: a.py\n-b = 2\n+b = 3\n*** End Patch\n"); err != nil || got != "a = 1\nb = 3" {
		t.Fatalf("no final newline: got %q, %v", got, err)
	}
	src := "def f():\n    a = 1\n\n    return a\n"
	patch := "*** Begin Patch\n*** Update File: a.py\n@@ def f():\n     a = 1\n\n-    return a\n+    return a + 1\n*** End Patch\n"
	if got, err := ApplyDiffPatchDirect(src, patch); err != nil || got != "def f():\n    a = 1\n\n    return a + 1\n" {
		t.Fatalf("blank context: got %q, %v", got, err)
	}
}

// Through the workspace HTTP handler with a real file, as agents call it.
func TestDiffPatchDocumentAppliesBeginPatchFormat(t *testing.T) {
	docsDir, cleanup := setupTestDocsDir(t)
	defer cleanup()
	gin.SetMode(gin.TestMode)
	viper.Set("docs-dir", docsDir)
	dir := filepath.Join(docsDir, "Workflow", "wf", "code", "shared")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "lib.py"), []byte(applyPatchLib), 0o644); err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.PATCH("/api/documents/*filepath", HandleDocumentRequest)
	patch := "*** Begin Patch\n*** Update File: Workflow/wf/code/shared/lib.py\n@@ def open_assets(\n-    page.click(\"#assets\")\n+    page.click(\"[data-testid=assets]\")\n*** End Patch\n"
	body, _ := json.Marshal(map[string]string{"diff": patch})
	req := httptest.NewRequest(http.MethodPatch, "/api/documents/Workflow/wf/code/shared/lib.py/diff", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	written, err := os.ReadFile(filepath.Join(dir, "lib.py"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(written), "[data-testid=assets]") || !strings.HasSuffix(string(written), "    return select_cases([1, 0, 2])\n") {
		t.Fatalf("file on disk:\n%s", written)
	}
}

func patchDocumentForTest(t *testing.T, docsDir, relPath string, body map[string]interface{}) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	viper.Set("docs-dir", docsDir)
	router := gin.New()
	router.PATCH("/api/documents/*filepath", HandleDocumentRequest)
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPatch, "/api/documents/"+relPath+"/diff", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

// A dry run reports whether the patch applies and writes nothing, not even a
// new file's folder; multi-file patches check every file this way first.
func TestDiffPatchDocumentDryRunWritesNothing(t *testing.T) {
	docsDir, cleanup := setupTestDocsDir(t)
	defer cleanup()
	path := filepath.Join(docsDir, "Workflow", "wf", "lib.py")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(applyPatchLib), 0o644); err != nil {
		t.Fatal(err)
	}
	patch := "*** Begin Patch\n*** Update File: lib.py\n-import os\n+import sys\n*** End Patch\n"
	w := patchDocumentForTest(t, docsDir, "Workflow/wf/lib.py", map[string]interface{}{"diff": patch, "dry_run": true})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"dry_run":true`) {
		t.Fatalf("dry run = %d %s", w.Code, w.Body.String())
	}
	if got, _ := os.ReadFile(path); string(got) != applyPatchLib {
		t.Fatal("dry run changed the file")
	}
	add := "*** Begin Patch\n*** Add File: new/ids.py\n+X = 1\n*** End Patch\n"
	w = patchDocumentForTest(t, docsDir, "Workflow/wf/new/ids.py", map[string]interface{}{"diff": add, "dry_run": true})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"would_create":true`) {
		t.Fatalf("dry run add = %d %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(filepath.Join(docsDir, "Workflow", "wf", "new")); !os.IsNotExist(err) {
		t.Fatal("dry run created the new file's folder")
	}
	bad := "*** Begin Patch\n*** Update File: lib.py\n-not in the file\n+x\n*** End Patch\n"
	if w := patchDocumentForTest(t, docsDir, "Workflow/wf/lib.py", map[string]interface{}{"diff": bad, "dry_run": true}); w.Code == http.StatusOK {
		t.Fatalf("a patch that does not apply must fail its dry run: %s", w.Body.String())
	}
}

// Hunks with no @@ line and Add File sections change the line count without
// an @@ header; the post-apply line-count check must count them.
func TestDiffPatchDocumentAcceptsBeginPatchWithoutAnchorsAndAddFile(t *testing.T) {
	docsDir, cleanup := setupTestDocsDir(t)
	defer cleanup()
	path := filepath.Join(docsDir, "Workflow", "wf", "lib.py")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(applyPatchLib), 0o644); err != nil {
		t.Fatal(err)
	}
	patch := "*** Begin Patch\n*** Update File: lib.py\n import os\n+import sys\n+import json\n*** End Patch\n"
	if w := patchDocumentForTest(t, docsDir, "Workflow/wf/lib.py", map[string]interface{}{"diff": patch}); w.Code != http.StatusOK {
		t.Fatalf("no-anchor patch = %d %s", w.Code, w.Body.String())
	}
	if got, _ := os.ReadFile(path); !strings.HasPrefix(string(got), "import os\nimport sys\nimport json\n") {
		t.Fatalf("file = %q", got)
	}
	add := "*** Begin Patch\n*** Add File: ids.py\n+A = 1\n+B = 2\n*** End Patch\n"
	if w := patchDocumentForTest(t, docsDir, "Workflow/wf/ids.py", map[string]interface{}{"diff": add}); w.Code != http.StatusOK {
		t.Fatalf("add file = %d %s", w.Code, w.Body.String())
	}
	if got, _ := os.ReadFile(filepath.Join(docsDir, "Workflow", "wf", "ids.py")); string(got) != "A = 1\nB = 2\n" {
		t.Fatalf("added file = %q", got)
	}
}
