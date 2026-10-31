package handlers

import (
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
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
		value, wholeCrew := c.Get("managed-crew-removal")
		if wholeCrew {
			file, err = value.(crewroot.Roots).Resources, nil
		}

		if err != nil {
			log.Printf("[FILE_MUTATION] user_id=%s stage=resolve path=%q error=%q", getUserID(c), input, err.Error())
			c.JSON(400, gin.H{"error": "invalid mutation path"})
			return nil, false
		}
		if err := crewroot.CheckRootMutation(viper.GetString("docs-dir"), file); !wholeCrew && err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return nil, false
		}
		if err := crewroot.CheckSyncWrite(viper.GetString("docs-dir"), file); !wholeCrew && err != nil {
			c.JSON(409, gin.H{"error": err.Error()})
			return nil, false
		}
		root, err := wf.MutationRoot(viper.GetString("docs-dir"), file)
		if wholeCrew {
			root, err = value.(crewroot.Roots).Files, nil
		}
		if err != nil {
			log.Printf("[FILE_MUTATION] user_id=%s stage=root path=%q resolved=%q error=%q", getUserID(c), input, file, err.Error())
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
		started := time.Now()
		unlock, err := wf.LockWorkspace(c.Request.Context(), root)
		if err != nil {
			release()
			log.Printf("[FILE_MUTATION] user_id=%s stage=lock root=%q wait_ms=%d error=%q", getUserID(c), root, time.Since(started).Milliseconds(), err.Error())
			c.JSON(503, gin.H{"error": "file edit serialization unavailable"})
			return nil, false
		}
		acquired := time.Now()
		wait := acquired.Sub(started)
		releases = append(releases, func() {
			unlock()
			held := time.Since(acquired)
			if wait >= time.Second || held >= time.Second {
				log.Printf("[FILE_MUTATION] user_id=%s stage=release root=%q wait_ms=%d held_ms=%d", getUserID(c), root, wait.Milliseconds(), held.Milliseconds())
			}
		})
	}
	c.Set("managed-file-lock", true)
	return func() { c.Set("managed-file-lock", false); release() }, true
}
