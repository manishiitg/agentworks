package handlers

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/manishiitg/coding-agent-loop/workspace/dashboards"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
	"github.com/spf13/viper"
)

// DashboardOperation is service-token only. Project authorization and the
// connection's effective draft/file rights are supplied by the agent service.
func DashboardOperation(c *gin.Context) {
	var req dashboards.Request
	decoder := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, 16<<20))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&req) != nil || decoder.Decode(new(any)) != io.EOF {
		c.JSON(400, gin.H{"error": "invalid dashboard request"})
		return
	}
	result, err := dashboards.Execute(c.Request.Context(), viper.GetString("docs-dir"), req)
	if err != nil {
		code, message := wf.ErrorDetails(err)
		if wf.StatusCode(err) == 500 {
			message = "dashboard operation failed"
		}
		c.JSON(wf.StatusCode(err), gin.H{"error": message, "code": code})
		return
	}
	c.JSON(200, result)
}
