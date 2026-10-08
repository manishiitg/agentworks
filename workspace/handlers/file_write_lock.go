package handlers

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
	"github.com/spf13/viper"
)

// lockFileMutation serializes managed document, patch, move, delete and upload
// operations with guarded MCP writes, including calls in another process.
func lockFileMutation(c *gin.Context, paths ...string) (func(), bool) {
	if held, _ := c.Get("managed-file-lock"); held == true {
		return func() {}, true
	}
	if len(paths) == 0 {
		paths = []string{c.Param("filepath")}
	}
	unique := map[string]bool{}
	for _, input := range paths {
		file, err := resolveUserPath(c, strings.TrimPrefix(input, "/"))
		if err != nil {
			c.JSON(400, gin.H{"error": "invalid mutation path"})
			return nil, false
		}
		root, err := wf.MutationRoot(viper.GetString("docs-dir"), file)
		if err != nil {
			c.JSON(400, gin.H{"error": "invalid mutation path"})
			return nil, false
		}
		// A new project has no files to serialize yet; lock its existing parent.
		for {
			if _, err := os.Stat(root); !os.IsNotExist(err) {
				break
			}
			root = filepath.Dir(root)
		}
		unique[root] = true
	}
	roots := make([]string, 0, len(unique))
	for root := range unique {
		roots = append(roots, root)
	}
	sort.Strings(roots)
	var releases []func()
	release := func() {
		for i := len(releases) - 1; i >= 0; i-- {
			releases[i]()
		}
	}
	for _, root := range roots {
		unlock, err := wf.LockWorkspace(c.Request.Context(), root)
		if err != nil {
			release()
			c.JSON(503, gin.H{"error": "file edit serialization unavailable"})
			return nil, false
		}
		releases = append(releases, unlock)
	}
	c.Set("managed-file-lock", true)
	return func() { c.Set("managed-file-lock", false); release() }, true
}
