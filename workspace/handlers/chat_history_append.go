package handlers

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/manishiitg/coding-agent-loop/workspace/chatlog"
	"github.com/manishiitg/coding-agent-loop/workspace/models"
	"github.com/manishiitg/coding-agent-loop/workspace/utils"
	"github.com/spf13/viper"
)

// AppendConversationHistoryRequest adds messages to a chat conversation and
// sets top-level fields (e.g. updated_at, revision) without rewriting it.
type AppendConversationHistoryRequest struct {
	Messages []json.RawMessage          `json:"messages"`
	Patch    map[string]json.RawMessage `json:"patch,omitempty"`
}

// AppendConversationHistory handles POST /api/documents/*filepath/append-history
// (Goals builder chats, which keep the header + history-log format until Goals
// are removed).
func AppendConversationHistory(c *gin.Context) {
	var req AppendConversationHistoryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{Success: false, Message: "Invalid request body", Error: err.Error()})
		return
	}
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
	if err := chatlog.Append(filePath, req.Messages, req.Patch); err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse[any]{Success: false, Message: "Failed to append conversation history", Error: err.Error()})
		return
	}
	c.JSON(http.StatusOK, models.APIResponse[models.Document]{Success: true, Message: "Conversation history appended", Data: models.Document{FilePath: filePathParam}})
}

// AppendChatTurnRequest adds turns to a chat folder and patches its header.
// Mode "extend" adds messages to the stored turn with the same id.
type AppendChatTurnRequest struct {
	Turns []chatlog.Turn             `json:"turns,omitempty"`
	Patch map[string]json.RawMessage `json:"patch,omitempty"`
	Mode  string                     `json:"mode,omitempty"`
	// OnlyMissing appends only turns whose ids the chat lacks (sync, retries).
	OnlyMissing bool `json:"only_missing,omitempty"`
}

// AppendChatTurn handles POST /api/documents/*filepath/append-turn on a chat
// folder's header.json. Chat history is never rewritten through PUT.
func AppendChatTurn(c *gin.Context) {
	var req AppendChatTurnRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{Success: false, Message: "Invalid request body", Error: err.Error()})
		return
	}
	docsDir := viper.GetString("docs-dir")
	filePathParam := utils.SanitizeInputPath(c.Param("filepath"), docsDir)
	filePath, err := resolveUserPath(c, filePathParam)
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse[any]{Success: false, Message: "Failed to resolve path", Error: err.Error()})
		return
	}
	if !utils.IsValidFilePath(filePath, docsDir) || !chatlog.IsChatHeaderPath(filePath) {
		c.JSON(http.StatusBadRequest, models.APIResponse[any]{Success: false, Message: "Invalid chat path", Error: "append-turn only applies to session-<id>/header.json"})
		return
	}
	started := time.Now()
	dir := chatlog.ChatDir(filePath)
	var stats chatlog.SaveStats
	if req.OnlyMissing {
		stats, err = chatlog.AppendMissingTurns(dir, req.Turns, req.Patch)
	} else {
		stats, err = chatlog.AppendTurns(dir, req.Turns, req.Patch, req.Mode)
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, models.APIResponse[any]{Success: false, Message: "Failed to append chat turn", Error: err.Error()})
		return
	}
	log.Printf("[CHAT_HISTORY] append path=%s turns_appended=%d bytes=%d header_bytes=%d turn_count=%d ms=%d", filePathParam, stats.Appended, stats.Bytes, stats.HeaderBytes, stats.TurnCount, time.Since(started).Milliseconds())
	c.JSON(http.StatusOK, models.APIResponse[chatlog.SaveStats]{Success: true, Message: "Chat turn appended", Data: stats})
}
