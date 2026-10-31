package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"

	"github.com/gin-gonic/gin"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
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
	var prefix string
	req.Root, req.Path, req.Destination, req.Guard, prefix, err = wf.BoundCrewFileRequest(docs, req.Root, req.Path, req.Destination, req.Guard)
	if err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	if physical, found, err := crewroot.Resolve(docs, req.Root+"/"+req.Path); err != nil || found && crewroot.CheckSyncWrite(docs, physical) != nil {
		c.JSON(409, gin.H{"error": "Project editing is on the other computer. Move editing here before changing files."})
		return
	}
	if req.Destination != "" {
		if physical, found, err := crewroot.Resolve(docs, req.Root+"/"+req.Destination); err != nil || found && crewroot.CheckSyncWrite(docs, physical) != nil {
			c.JSON(409, gin.H{"error": "Project editing is on the other computer."})
			return
		}
	}
	state, err := wf.DefaultStateDir(docs)
	if err != nil {
		c.AbortWithStatus(503)
		return
	}
	var editor *wf.Editor
	if physical, bound, resolveErr := crewroot.ScopeRoot(docs, req.Root, req.Path); bound || resolveErr != nil {
		if resolveErr != nil {
			c.JSON(400, gin.H{"error": resolveErr.Error()})
			return
		}
		state, err = wf.DefaultStateDir(physical)
		if err == nil {
			editor, err = wf.OpenScopedEditor(physical, state, req.Root)
		}
	} else {
		editor, err = wf.OpenEditor(docs, state)
	}
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
	if prefix != "" {
		req.Path = prefix + "/" + req.Path
		if req.Destination != "" {
			req.Destination = prefix + "/" + req.Destination
		}
	}
	c.JSON(200, gin.H{"action": req.Action, "path": req.Path, "destination": req.Destination, "applied": true})
}
