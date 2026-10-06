package handlers

import (
	"github.com/gin-gonic/gin"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
	"github.com/spf13/viper"
)

// lockFileMutation serializes managed document, patch, move, delete and upload
// operations with guarded MCP writes, including calls in another process.
func lockFileMutation(c *gin.Context) (func(), bool) {
	if held, _ := c.Get("managed-file-lock"); held == true {
		return func() {}, true
	}
	release, err := wf.LockWorkspace(c.Request.Context(), viper.GetString("docs-dir"))
	if err != nil {
		c.JSON(503, gin.H{"error": "file edit serialization unavailable"})
		return nil, false
	}
	c.Set("managed-file-lock", true)
	return func() { c.Set("managed-file-lock", false); release() }, true
}
