package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"

	"github.com/gin-gonic/gin"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
	"github.com/spf13/viper"
)

// SharedFileLifecycle is service-authenticated like SharedFileWrite. It never
// exposes the generic unguarded document/folder mutation routes externally.
func SharedFileLifecycle(c *gin.Context) {
	var req wf.LifecycleRequest
	decoder := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, 32<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&req) != nil || decoder.Decode(new(any)) != io.EOF {
		c.JSON(400, gin.H{"error": "invalid request"})
		return
	}
	docs, err := filepath.Abs(viper.GetString("docs-dir"))
	if err != nil {
		c.AbortWithStatus(503)
		return
	}
	state, err := wf.DefaultStateDir(docs)
	if err != nil {
		c.AbortWithStatus(503)
		return
	}
	editor, err := wf.OpenEditor(docs, state)
	if err != nil {
		c.AbortWithStatus(503)
		return
	}
	defer editor.Close()
	if err = editor.Lifecycle(c.Request.Context(), req); err != nil {
		code, message := wf.ErrorDetails(err)
		c.JSON(wf.StatusCode(err), gin.H{"error": message, "code": code})
		return
	}
	c.JSON(200, gin.H{"action": req.Action, "path": req.Path, "destination": req.Destination, "applied": true})
}
