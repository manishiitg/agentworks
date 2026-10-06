package skills

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeWorkspaceServer minimally implements the workspace API endpoints
// that runtime_loader.go hits: GET /api/documents?folder=<path> for
// directory listings and GET /api/documents/<path> for file contents.
// Routes are matched by suffix so tests can register fixtures by their
// workspace-relative paths.
//
// Why a real httptest.Server instead of mocking WorkspaceAPIClient:
// LoadAttachable holds a *WorkspaceAPIClient internally and there's no
// dependency-injection seam to swap it. Going through the HTTP layer
// also catches any URL-encoding / response-shape regressions that pure
// in-memory mocks would miss.
func fakeWorkspaceServer(t *testing.T, files map[string]string, listings map[string][]DocumentEntry) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/skills/workspace/files", func(w http.ResponseWriter, r *http.Request) {
		var req map[string]string
		_ = json.NewDecoder(r.Body).Decode(&req)
		supporting := []struct {
			RelPath string
			Content []byte
		}{}
		for file, content := range files {
			rel := strings.TrimPrefix(file, req["workspace_path"]+"/")
			prefix := "skills/" + req["name"] + "/"
			if strings.HasPrefix(rel, prefix) && !strings.HasSuffix(rel, "/SKILL.md") {
				supporting = append(supporting, struct {
					RelPath string
					Content []byte
				}{strings.TrimPrefix(rel, prefix), []byte(content)})
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"files": supporting})
	})
	mux.HandleFunc("/api/skills/workspace/list", func(w http.ResponseWriter, r *http.Request) {
		var req map[string]string
		_ = json.NewDecoder(r.Body).Decode(&req)
		documents := []map[string]interface{}{}
		for file, content := range files {
			relative := strings.TrimPrefix(file, req["workspace_path"]+"/")
			if strings.HasSuffix(relative, "/SKILL.md") && strings.HasPrefix(relative, "skills/") {
				name := strings.TrimSuffix(strings.TrimPrefix(relative, "skills/"), "/SKILL.md")
				documents = append(documents, map[string]interface{}{"folder_name": name, "file_path": relative, "document": content})
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"documents": documents})
	})
	mux.HandleFunc("/api/documents", func(w http.ResponseWriter, r *http.Request) {
		folder := r.URL.Query().Get("folder")
		if _, ok := listings[folder]; !ok {
			folder = strings.TrimPrefix(folder, "Workflow/test/")
		}
		children, ok := listings[folder]
		if strings.HasPrefix(r.URL.Query().Get("folder"), "Workflow/test/") {
			var prefix func([]DocumentEntry) []DocumentEntry
			prefix = func(rows []DocumentEntry) []DocumentEntry {
				out := append([]DocumentEntry(nil), rows...)
				for i := range out {
					out[i].Filepath = "Workflow/test/" + out[i].Filepath
					out[i].Children = prefix(out[i].Children)
				}
				return out
			}
			children = prefix(children)
		}
		if !ok {
			http.Error(w, "no listing fixture for "+folder, http.StatusNotFound)
			return
		}
		// Mirror the unwrap shape the real workspace returns: data is a
		// single-element array wrapping the requested folder, with
		// children inside.
		resp := DocumentsResponse{
			Success: true,
			Data: []DocumentEntry{{
				Filepath: folder,
				Type:     "folder",
				Children: children,
			}},
		}
		_ = json.NewEncoder(w).Encode(resp)
	})
	mux.HandleFunc("/api/documents/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/documents/")
		if _, ok := files[path]; !ok {
			path = strings.TrimPrefix(path, "Workflow/test/")
		}
		content, ok := files[path]
		if !ok {
			http.Error(w, "no file fixture for "+path, http.StatusNotFound)
			return
		}
		// Encode via raw JSON to avoid having to redeclare the
		// anonymous Data struct's exact tag set when it drifts.
		payload := map[string]interface{}{
			"success": true,
			"data":    map[string]interface{}{"content": content},
		}
		_ = json.NewEncoder(w).Encode(payload)
	})
	return httptest.NewServer(mux)
}

func TestLoadAttachableBuildsSkillFromWorkspace(t *testing.T) {
	files := map[string]string{
		"skills/pdf-extract/SKILL.md":          "---\nname: pdf-extract\ndescription: Extract text from PDFs\n---\n\n# PDF Extract\n\nUse this skill to extract structured text from PDF files.\n",
		"skills/pdf-extract/scripts/run.py":    "print('hi')\n",
		"skills/pdf-extract/references/api.md": "# API Reference\n\nDetails on the extraction API.\n",
	}
	listings := map[string][]DocumentEntry{
		"skills/pdf-extract": {
			{Filepath: "skills/pdf-extract/SKILL.md", Type: "file"},
			{Filepath: "skills/pdf-extract/scripts", Type: "folder", Children: []DocumentEntry{
				{Filepath: "skills/pdf-extract/scripts/run.py", Type: "file"},
			}},
			{Filepath: "skills/pdf-extract/references", Type: "folder", Children: []DocumentEntry{
				{Filepath: "skills/pdf-extract/references/api.md", Type: "file"},
			}},
		},
	}
	srv := fakeWorkspaceServer(t, files, listings)
	defer srv.Close()

	got := LoadAttachableIn(srv.URL, "Workflow/test", []string{"pdf-extract"})
	if len(got) != 1 {
		t.Fatalf("expected 1 skill loaded, got %d", len(got))
	}
	skill := got[0]
	if skill.Name != "pdf-extract" {
		t.Errorf("expected Name=pdf-extract, got %q", skill.Name)
	}
	if skill.Description != "Extract text from PDFs" {
		t.Errorf("expected description from frontmatter, got %q", skill.Description)
	}
	if !strings.Contains(skill.Content, "# PDF Extract") {
		t.Errorf("expected body content, got %q", skill.Content)
	}
	if len(skill.SupportingFiles) != 2 {
		t.Fatalf("expected 2 supporting files (scripts/run.py + references/api.md), got %d: %+v", len(skill.SupportingFiles), skill.SupportingFiles)
	}
	// SKILL.md must NOT appear as a supporting file — its body is in
	// skill.Content. Adapters re-materialize SKILL.md from the
	// structured fields.
	for _, sf := range skill.SupportingFiles {
		if strings.EqualFold(sf.RelPath, "SKILL.md") {
			t.Errorf("SKILL.md leaked into SupportingFiles: %+v", sf)
		}
	}
}

func TestLoadAttachableInLoadsWorkspaceScopedSupportingFiles(t *testing.T) {
	files := map[string]string{
		"Workflow/demo/skills/basic-browser-setup/SKILL.md":            "---\nname: basic-browser-setup\ndescription: Configure browser QA\n---\nbody\n",
		"Workflow/demo/skills/basic-browser-setup/references/setup.md": "# Setup\n",
	}
	listings := map[string][]DocumentEntry{
		"Workflow/demo/skills/basic-browser-setup": {
			{Filepath: "Workflow/demo/skills/basic-browser-setup/SKILL.md", Type: "file"},
			{Filepath: "Workflow/demo/skills/basic-browser-setup/references", Type: "folder", Children: []DocumentEntry{
				{Filepath: "Workflow/demo/skills/basic-browser-setup/references/setup.md", Type: "file"},
			}},
		},
	}
	srv := fakeWorkspaceServer(t, files, listings)
	defer srv.Close()

	got := LoadAttachableIn(srv.URL, "Workflow/demo", []string{"basic-browser-setup"})
	if len(got) != 1 || len(got[0].SupportingFiles) != 1 {
		t.Fatalf("workspace playbook bundle was not attached: %+v", got)
	}
	if got[0].SupportingFiles[0].RelPath != "references/setup.md" {
		t.Fatalf("supporting path = %q", got[0].SupportingFiles[0].RelPath)
	}
}

func TestLoadAttachableLoadsAgentBrowserSkill(t *testing.T) {
	got := LoadAttachable("http://unused.example", []string{"agent-browser"})
	if len(got) != 1 {
		t.Fatalf("expected agent-browser skill to load, got %+v", got)
	}
	if got[0].Name != "agent-browser" ||
		!strings.Contains(got[0].Content, "CDP Shared Chrome Rules") ||
		!strings.Contains(got[0].Content, "bash -s -- --port 9333") ||
		!strings.Contains(got[0].Content, "one hour after the final run") ||
		!strings.Contains(got[0].Content, `browser("tab", ["close", "<owned-label-or-tN>"])`) ||
		!strings.Contains(got[0].Content, "exact-URL reuse check atomically") ||
		!strings.Contains(got[0].Content, "The live step prompt and folder guard are authoritative") ||
		!strings.Contains(got[0].Content, "Never close a pre-existing user tab") {
		t.Errorf("unexpected loaded skill: %+v", got[0])
	}
}

func TestLoadAttachableSkipsMissingSkills(t *testing.T) {
	// A skill folder name in the selection that doesn't exist in the
	// workspace must not crash the load — log and skip, return the rest.
	srv := fakeWorkspaceServer(t,
		map[string]string{
			"skills/real/SKILL.md": "---\nname: real\ndescription: A real skill\n---\nbody\n",
		},
		map[string][]DocumentEntry{
			"skills/real": {{Filepath: "skills/real/SKILL.md", Type: "file"}},
		},
	)
	defer srv.Close()

	got := LoadAttachableIn(srv.URL, "Workflow/test", []string{"real", "does-not-exist"})
	if len(got) != 1 {
		t.Fatalf("expected 1 skill (real, with missing skipped), got %d", len(got))
	}
	if got[0].Name != "real" {
		t.Errorf("expected real skill, got %q", got[0].Name)
	}
}

func TestLoadAttachableHandlesEmptySelection(t *testing.T) {
	if got := LoadAttachable("http://unused.example", nil); got != nil {
		t.Errorf("expected nil for nil selection, got %+v", got)
	}
	if got := LoadAttachable("http://unused.example", []string{}); got != nil {
		t.Errorf("expected nil for empty selection, got %+v", got)
	}
}

func TestWithAgentBrowserCapability(t *testing.T) {
	t.Run("adds built-in skill when browser tool is present", func(t *testing.T) {
		selected := []string{"pdf-extract"}
		got := WithAgentBrowserCapability(selected, true)
		if strings.Join(got, ",") != "pdf-extract,agent-browser" {
			t.Fatalf("unexpected skills: %v", got)
		}
		if len(selected) != 1 {
			t.Fatalf("persisted selection was mutated: %v", selected)
		}
	})

	t.Run("does not duplicate explicit selection", func(t *testing.T) {
		got := WithAgentBrowserCapability([]string{"agent-browser", "pdf-extract"}, true)
		if strings.Join(got, ",") != "agent-browser,pdf-extract" {
			t.Fatalf("unexpected skills: %v", got)
		}
	})

	t.Run("leaves skills unchanged without browser tool", func(t *testing.T) {
		got := WithAgentBrowserCapability([]string{"pdf-extract"}, false)
		if strings.Join(got, ",") != "pdf-extract" {
			t.Fatalf("unexpected skills: %v", got)
		}
	})
}

func TestLazySkillBody(t *testing.T) {
	t.Run("small body injected whole", func(t *testing.T) {
		body := strings.Repeat("line\n", 100)
		if got := lazySkillBody("skills/small/SKILL.md", "small", body); got != body {
			t.Fatalf("small body should pass through unchanged")
		}
	})

	t.Run("large body becomes excerpt plus pointer", func(t *testing.T) {
		var sb strings.Builder
		for i := 0; i < 400; i++ {
			fmt.Fprintf(&sb, "line %d\n", i)
		}
		got := lazySkillBody("skills/big/SKILL.md", "big", sb.String())
		if !strings.Contains(got, "line 0") || strings.Contains(got, "line 200") {
			t.Fatalf("excerpt should keep the head and drop the tail:\n%s", got)
		}
		if !strings.Contains(got, "skills/big/SKILL.md") {
			t.Fatalf("pointer should name the on-disk SKILL.md path:\n%s", got)
		}
		if !strings.Contains(got, "excerpt") {
			t.Fatalf("pointer should say it is an excerpt:\n%s", got)
		}
	})

	t.Run("empty filePath falls back to folder convention", func(t *testing.T) {
		var sb strings.Builder
		for i := 0; i < 400; i++ {
			fmt.Fprintf(&sb, "line %d\n", i)
		}
		got := lazySkillBody("", "fallback-skill", sb.String())
		if !strings.Contains(got, "skills/fallback-skill/SKILL.md") {
			t.Fatalf("fallback pointer path missing:\n%s", got)
		}
	})
}
