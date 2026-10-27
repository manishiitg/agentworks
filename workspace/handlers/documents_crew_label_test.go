package handlers

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/coding-agent-loop/workspace/models"
	"github.com/spf13/viper"
)

func setupLocalCrewDocuments(t *testing.T) (string, string) {
	t.Helper()
	base := t.TempDir()
	docs, project, state := filepath.Join(base, "docs"), filepath.Join(base, "existing-project"), filepath.Join(base, "state")
	for _, dir := range []string{filepath.Join(docs, "Crew", "example"), filepath.Join(project, ".agentworks"), filepath.Join(state, "ownership")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	docs, _ = filepath.EvalSymlinks(docs)
	project, _ = filepath.EvalSymlinks(project)
	t.Setenv("LOCAL_MODE", "true")
	t.Setenv("AGENTWORKS_STATE_ROOT", state)
	raw, _ := json.Marshal(map[string]any{"projects": map[string]crewroot.Record{"work/example": {Product: "work", Folder: "example", Owner: "owner", ID: "example", LocalRoot: project}}})
	if err := os.WriteFile(filepath.Join(state, "ownership", "projects.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := crewroot.EnsurePrivateRoot(docs, "example", filepath.Join(docs, "Crew", "example")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "notes.txt"), []byte("project notes"), 0600); err != nil {
		t.Fatal(err)
	}
	previous := viper.Get("docs-dir")
	t.Cleanup(func() { viper.Set("docs-dir", previous) })
	return docs, project
}

func TestLocalCrewDocumentsLabelKeepsIssuedPaths(t *testing.T) {
	docs, project := setupLocalCrewDocuments(t)
	router := setupRouter(docs)
	response := listDocs(t, router, "Crew/example/_files", "owner")
	var labeled int
	for _, doc := range response.Data {
		if doc.FilePath == "Crew/example/_files" {
			labeled++
			if doc.DisplayName != "existing-project" || doc.Type != "folder" {
				t.Fatalf("wrong root label: %+v", doc)
			}
		} else if doc.DisplayName != "" {
			t.Fatalf("child received a root label: %+v", doc)
		}
	}
	if labeled != 1 {
		t.Fatalf("expected one labeled root, got %d", labeled)
	}
	// The label is presentation only: existing reads still use the issued path.
	resolved, bound, err := crewroot.Resolve(docs, "Crew/example/_files/notes.txt")
	if err != nil || !bound || resolved != filepath.Join(project, "notes.txt") {
		t.Fatalf("file routing changed: %s, %t, %v", resolved, bound, err)
	}
}

func TestLazyCrewListingsLookAheadAndSearchUnloadedFiles(t *testing.T) {
	docs, project := setupLocalCrewDocuments(t)
	root := "Crew/example/_files"
	for repo := 0; repo < 18; repo++ {
		for folder := 0; folder < 5; folder++ {
			dir := filepath.Join(project, fmt.Sprintf("repo-%02d/src/section-%02d", repo, folder))
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			for file := 0; file < 20; file++ {
				if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("module-%02d.ts", file)), []byte("export {}"), 0600); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	for _, dir := range []string{"empty", "blocked-only/private", ".hidden", "uncertain"} {
		if err := os.MkdirAll(filepath.Join(project, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"blocked-only/private/data.txt", ".hidden/data.txt"} {
		if err := os.WriteFile(filepath.Join(project, name), []byte("not listed"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(project, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "missing"), filepath.Join(project, "uncertain", "broken")); err != nil {
		t.Fatal(err)
	}
	router := setupRouter(docs)
	router.GET("/api/search", SearchDocuments)
	read := func(params url.Values) models.APIResponse[[]models.Document] {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest("GET", "/api/documents?"+params.Encode(), nil))
		var response models.APIResponse[[]models.Document]
		if recorder.Code != 200 || json.Unmarshal(recorder.Body.Bytes(), &response) != nil || !response.Success {
			t.Fatal(recorder.Body.String())
		}
		return response
	}
	params := url.Values{"folder": {root}, "max_depth": {"0"}, "view_root": {root}, "hide_dotfiles": {"true"}, "blocked_paths": {root + "/blocked-only/private"}}
	started := time.Now()
	response := read(params)
	shallowTime := time.Since(started)
	nodes := map[string]models.Document{}
	var collect func([]models.Document)
	collect = func(items []models.Document) {
		for _, item := range items {
			nodes[item.FilePath] = item
			collect(item.Children)
		}
	}
	collect(response.Data)
	for path := range nodes {
		if path != root && strings.Contains(strings.TrimPrefix(path, root+"/"), "/") {
			t.Fatalf("returned grandchildren: %s", path)
		}
	}
	for _, suffix := range []string{"escape", ".agentworks", ".hidden"} {
		if _, exists := nodes[root+"/"+suffix]; exists {
			t.Fatalf("disclosed excluded entry %s", suffix)
		}
	}
	for _, suffix := range []string{"empty", "blocked-only"} {
		hint := nodes[root+"/"+suffix].HasChildren
		if hint == nil || *hint {
			t.Fatalf("wrong empty hint for %s", suffix)
		}
	}
	if hint := nodes[root+"/repo-00"].HasChildren; hint == nil || !*hint {
		t.Fatal("nonempty folder lost arrow")
	}
	if hint := nodes[root+"/uncertain"].HasChildren; hint != nil {
		t.Fatal("failed peek falsely claims empty")
	}
	// The next expansion uses the same direct-child API, not a recursive read.
	params.Set("folder", root+"/repo-00")
	expanded := read(params)
	if len(expanded.Data) == 0 {
		t.Fatal("expanded branch missing")
	}
	// Search reaches a leaf that has never been loaded into the browser tree.
	recorder := httptest.NewRecorder()
	query := url.Values{"folder": {root}, "search_type": {"filename"}, "query": {"module-19.ts"}, "limit": {"500"}}
	router.ServeHTTP(recorder, httptest.NewRequest("GET", "/api/search?"+query.Encode(), nil))
	var search models.APIResponse[models.SearchResponse]
	if recorder.Code != 200 || json.Unmarshal(recorder.Body.Bytes(), &search) != nil || search.Data.Total != 90 {
		t.Fatalf("incomplete unloaded search: %s", recorder.Body.String())
	}
	params.Set("folder", root)
	params.Set("max_depth", "-1")
	started = time.Now()
	full := read(params)
	fullTime := time.Since(started)
	shallowCount := len(nodes)
	collect(full.Data)
	if len(nodes) <= shallowCount {
		t.Fatal("fixture did not exercise a larger tree")
	}
	t.Logf("18 repositories/1800 leaf files: shallow=%s full=%s root_entries=%d", shallowTime, fullTime, len(response.Data))
}
