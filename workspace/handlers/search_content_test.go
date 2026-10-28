package handlers

import (
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

// Files panel "Search in files": a match in an ordinary file is found, and
// private areas (Builder transcripts, secrets, hidden files, server state),
// skipped build folders, binary files and files over 2 MB are never read.
func TestContentSearchFindsTextButNeverReadsPrivateOrExcludedFiles(t *testing.T) {
	docs := t.TempDir()
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	old := viper.Get("docs-dir")
	viper.Set("docs-dir", docs)
	t.Cleanup(func() { viper.Set("docs-dir", old) })
	write := func(rel, content string) {
		full := filepath.Join(docs, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("Workflow/demo/notes/plan.md", "intro\n   launch the Rocket today   \n")
	for _, private := range []string{
		"Workflow/demo/builder/conversation.md",
		"Workflow/demo/secrets/token.txt",
		"Workflow/demo/.env",
		"Workflow/demo/node_modules/pkg/index.js",
		"Workflow/demo/dist/bundle.js",
		"_system/crew-data/demo/chat_history.md",
	} {
		write(private, "Rocket")
	}
	write("Workflow/demo/image.bin", "Rocket\x00\x01")
	write("Workflow/demo/huge.txt", "Rocket\n"+strings.Repeat("x", 2<<20))

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/api/search", SearchDocuments)
	search := func(params string) (int, string) {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/search?search_type=content&"+params, nil))
		return w.Code, w.Body.String()
	}
	for _, params := range []string{"folder=Workflow/demo&query=rocket", "query=rocket&whole_word=1", "folder=Workflow/demo&query=R.ck&regex=1&match_case=1"} {
		code, body := search(params)
		var out struct {
			Data struct {
				Results []struct {
					Filepath    string `json:"filepath"`
					LineNumber  int    `json:"line_number"`
					MatchedText string `json:"matched_text"`
				} `json:"results"`
			} `json:"data"`
		}
		if code != http.StatusOK || json.Unmarshal([]byte(body), &out) != nil || len(out.Data.Results) != 1 {
			t.Fatalf("%s: want exactly the one ordinary match, got %d %s", params, code, body)
		}
		if got := out.Data.Results[0]; got.Filepath != "Workflow/demo/notes/plan.md" || got.LineNumber != 2 || got.MatchedText != "launch the Rocket today" {
			t.Fatalf("%s: wrong match %+v", params, got)
		}
	}
	if code, body := search("folder=Workflow/demo&query=rocket&match_case=1"); code != http.StatusOK || strings.Contains(body, "plan.md") {
		t.Fatalf("match case must not match other case: %d %s", code, body)
	}
	if code, body := search("folder=Workflow/demo&query=(&regex=1"); code != http.StatusBadRequest {
		t.Fatalf("an invalid pattern must be refused, got %d %s", code, body)
	}
}
