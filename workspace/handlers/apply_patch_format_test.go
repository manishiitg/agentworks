package handlers

import (
	"strings"
	"testing"
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
