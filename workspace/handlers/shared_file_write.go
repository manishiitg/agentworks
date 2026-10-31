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

// SharedFileWrite is reachable only by the trusted agent service. It supplies
// the authenticated actor and live token guard after workflow authorization.
func SharedFileWrite(c *gin.Context) {
	var req wf.WriteRequest
	d := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, 16<<20))
	d.DisallowUnknownFields()
	if d.Decode(&req) != nil || d.Decode(new(any)) != io.EOF {
		c.JSON(400, gin.H{"error": "invalid request"})
		return
	}
	root, err := wf.CleanRelative(req.Root)
	if err != nil || root == "." {
		c.JSON(400, gin.H{"error": "workflow root required"})
		return
	}
	docs, err := filepath.Abs(viper.GetString("docs-dir"))
	if err != nil {
		c.AbortWithStatus(503)
		return
	}
	var prefix string
	req.Root, req.Path, _, req.Guard, prefix, err = wf.BoundCrewFileRequest(docs, req.Root, req.Path, "", req.Guard)
	if err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	if physical, found, err := crewroot.Resolve(docs, req.Root+"/"+req.Path); err != nil || found && crewroot.CheckSyncWrite(docs, physical) != nil {
		c.JSON(409, gin.H{"error": "Project editing is on the other computer. Move editing here before changing files."})
		return
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
		c.JSON(503, gin.H{"error": "file editor unavailable"})
		return
	}
	defer editor.Close()
	receipt, err := editor.WriteWithPatch(c.Request.Context(), req, ApplyDiffPatchDirect)
	if err != nil {
		code, message := wf.ErrorDetails(err)
		c.JSON(wf.StatusCode(err), gin.H{"error": message, "code": code})
		return
	}
	if prefix != "" {
		receipt.Path = prefix + "/" + receipt.Path
	}
	c.JSON(200, receipt)
}
