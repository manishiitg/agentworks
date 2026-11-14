package handlers

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/manishiitg/coding-agent-loop/workspace/chatlog"
	"github.com/manishiitg/coding-agent-loop/workspace/models"
	"github.com/manishiitg/coding-agent-loop/workspace/utils"
	"github.com/spf13/viper"
)

// AppendConversationHistoryRequest adds messages to a chat conversation and
// sets top-level fields (e.g. updated_at, revision) without rewriting it.
type AppendConversationHistoryRequest struct {
	Expected *chatlog.Version           `json:"expected_version,omitempty"`
	Messages []json.RawMessage          `json:"messages"`
	Patch    map[string]json.RawMessage `json:"patch,omitempty"`
}

// AppendConversationHistory handles POST /api/documents/*filepath/append-history.
func AppendConversationHistory(c *gin.Context) {
	var req AppendConversationHistoryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{Success: false, Message: "Invalid request body", Error: err.Error()})
		return
	}
	release, ok := lockFileMutation(c)
	if !ok {
		return
	}
	defer release()
	docsDir := viper.GetString("docs-dir")
	filePathParam := utils.SanitizeInputPath(c.Param("filepath"), docsDir)
	filePath, err := resolveUserPath(c, filePathParam)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse[any]{Success: false, Message: "Failed to resolve path", Error: err.Error()})
		return
	}
	if !utils.IsValidFilePath(filePath, docsDir) || !chatlog.IsConversationPath(filePath) {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{Success: false, Message: "Invalid conversation path", Error: "append-history only applies to *-conversation.json"})
		return
	}
	if err := chatlog.AppendIfVersion(filePath, req.Messages, req.Patch, req.Expected); err != nil {
		if errors.Is(err, chatlog.ErrSourceChanged) {
			c.JSON(http.StatusConflict, models.APIResponse[any]{Success: false, Message: "Conversation changed before append"})
			return
		}
		c.JSON(http.StatusInternalServerError, models.APIResponse[any]{Success: false, Message: "Failed to append conversation history", Error: err.Error()})
		return
	}
	c.JSON(http.StatusOK, models.APIResponse[models.Document]{Success: true, Message: "Conversation history appended", Data: models.Document{FilePath: filePathParam}})
}
