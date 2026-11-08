package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/manishiitg/coding-agent-loop/workspace/handlers"
	"github.com/manishiitg/coding-agent-loop/workspace/models"
	"github.com/spf13/viper"
)

// PLAT-746: measure the actual bulk HTTP paths, rather than multiplying one
// file check by the number of entries. The server walkers don't use this client
// validator. Run explicitly with -bench, not during the ordinary test suite.
func BenchmarkFolderGuardBulkFiles(b *testing.B) {
	docs := b.TempDir()
	b.Setenv("WORKSPACE_DOCS_PATH", docs)
	b.Setenv("AGENTWORKS_STATE_ROOT", b.TempDir())
	old := viper.Get("docs-dir")
	viper.Set("docs-dir", docs)
	b.Cleanup(func() { viper.Set("docs-dir", old) })
	const folder = "Workflow/perf"
	guard := &FolderGuardConfig{Enabled: true, ReadPaths: []string{folder}}
	for i := 0; i < 50; i++ {
		dir := filepath.Join(docs, folder, fmt.Sprintf("part-%02d", i))
		if err := os.MkdirAll(dir, 0755); err != nil {
			b.Fatal(err)
		}
		if i > 0 {
			guard.ReadPaths = append(guard.ReadPaths, filepath.Join(folder, fmt.Sprintf("part-%02d", i)))
		}
		for j := 0; j < 100; j++ {
			content := "ordinary fixture\n"
			if i == 49 && j == 99 {
				content = "unique-match\n"
			}
			if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("file-%02d.md", j)), []byte(content), 0600); err != nil {
				b.Fatal(err)
			}
		}
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/api/documents", handlers.ListDocuments)
	router.GET("/api/search", handlers.SearchDocuments)
	server := httptest.NewServer(router)
	defer server.Close()
	client := NewClient(server.URL, WithFolderGuard(guard))
	ctx := context.Background()
	depth := -1
	for _, baseline := range []bool{true, false} {
		label := "canonical"
		if baseline {
			label = "previous"
		}
		b.Run("listing-5000/"+label, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				var raw []byte
				var err error
				if baseline {
					// This relative Workflow path has no user alias: the previous
					// boundary check was exactly this unchanged core validator.
					if err = validatePathAgainstGuard(guard, folder, false); err == nil {
						raw, err = client.request(ctx, "GET", "/api/documents?folder="+folder+"&max_depth=-1", nil)
					}
				} else {
					var out ListFilesResult
					out, err = client.ListWorkspaceFiles(ctx, ListWorkspaceFilesParams{Folder: folder, MaxDepth: &depth})
					raw = out.Raw
				}
				if err != nil {
					b.Fatal(err)
				}
				var out struct {
					Data []models.Document `json:"data"`
				}
				if err := json.Unmarshal(raw, &out); err != nil {
					b.Fatal(err)
				}
				files := map[string]bool{}
				var visit func([]models.Document)
				visit = func(docs []models.Document) {
					for _, doc := range docs {
						if doc.Type != "folder" {
							files[doc.FilePath] = true
						}
						visit(doc.Children)
					}
				}
				visit(out.Data)
				if len(files) != 5000 {
					b.Fatalf("listing returned %d distinct files", len(files))
				}
			}
		})
		b.Run("recursive-search-5000/"+label, func(b *testing.B) {
			// Both versions use this same server-side search path; neither
			// invokes Client.ValidatePath for entries in the recursive walk.
			for i := 0; i < b.N; i++ {
				raw, err := client.request(ctx, "GET", "/api/search?folder="+folder+"&search_type=content&query=unique-match", nil)
				if err != nil {
					b.Fatal(err)
				}
				var out struct {
					Data struct {
						Total     int  `json:"total"`
						Truncated bool `json:"truncated"`
					} `json:"data"`
				}
				if err := json.Unmarshal(raw, &out); err != nil {
					b.Fatal(err)
				}
				if out.Data.Total != 1 || out.Data.Truncated {
					b.Fatalf("incomplete recursive search: %s", raw)
				}
			}
		})
	}
}
