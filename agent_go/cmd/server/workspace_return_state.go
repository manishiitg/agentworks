package server

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
)

type workspaceLayout struct {
	ChatOpen        bool    `json:"chat_open"`
	PanelOpen       bool    `json:"panel_open"`
	SplitRatio      float64 `json:"split_ratio"`
	NavigationMode  string  `json:"navigation_mode"`
	PreviewDevice   string  `json:"preview_device,omitempty"`
	FocusedPane     string  `json:"focused_pane,omitempty"`
	MobilePaneWidth float64 `json:"mobile_pane_width,omitempty"`
}
type workspaceChat struct {
	Name             string `json:"name"`
	ReadOnly         bool   `json:"read_only"`
	ExecutionRun     bool   `json:"execution_run,omitempty"`
	ScheduledRun     bool   `json:"scheduled_run,omitempty"`
	ScheduledJobName string `json:"scheduled_job_name,omitempty"`
	BotPlatform      string `json:"bot_platform,omitempty"`
	ViewMode         string `json:"view_mode,omitempty"`
	WorkshopMode     string `json:"workshop_mode,omitempty"`
	ConversationKey  string `json:"conversation_key,omitempty"`
}

type workspaceDestination struct {
	WorkspacePath string           `json:"workspace_path,omitempty"`
	Surface       string           `json:"surface"`
	ID            string           `json:"id,omitempty"`
	View          string           `json:"view,omitempty"`
	SessionID     string           `json:"session_id,omitempty"`
	Chat          *workspaceChat   `json:"chat,omitempty"`
	Layout        *workspaceLayout `json:"layout,omitempty"`
}
type workspaceReturnState struct {
	Version   int                    `json:"version"`
	Revision  uint64                 `json:"revision"`
	Last      *workspaceDestination  `json:"last"`
	Recent    []workspaceDestination `json:"recent"`
	UpdatedAt string                 `json:"updated_at,omitempty"`
	Note      string                 `json:"note,omitempty"` // response only
}

var workspaceReturnMu sync.Mutex
var workspaceReturnID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.:-]{0,255}$`)
var workspaceReturnView = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

func workspaceReturnPath(user string) string {
	return filepath.ToSlash(filepath.Join(workspaceref.PhysicalPath(user, "preferences"), "workspace-state.json"))
}
func validWorkspaceDestination(d *workspaceDestination) bool {
	if d == nil {
		return true
	}
	known := map[string]bool{"agentworks": true, "relays": true, "work": true, "code": true, "mcp-gateway": true, "llm-gateway": true, "knowledgebase": true, "bullpen": true, "video-studio": true, "sparkquill": true}
	if !known[d.Surface] || (d.ID != "" && (!workspaceReturnID.MatchString(d.ID) || d.ID == "." || d.ID == "..")) || (d.View != "" && !workspaceReturnView.MatchString(d.View)) || (d.SessionID != "" && !workspaceReturnID.MatchString(d.SessionID)) {
		return false
	}
	if d.Chat != nil && (len(d.Chat.Name) > 256 || len(d.Chat.ScheduledJobName) > 256 || len(d.Chat.BotPlatform) > 32 || (d.Chat.ViewMode != "" && d.Chat.ViewMode != "formatted" && d.Chat.ViewMode != "terminal" && d.Chat.ViewMode != "tree") || (d.Chat.WorkshopMode != "" && d.Chat.WorkshopMode != "workshop" && d.Chat.WorkshopMode != "run") || (d.Chat.ConversationKey != "" && (!workspaceReturnID.MatchString(d.Chat.ConversationKey) || !(d.Chat.ConversationKey == d.ID || strings.HasPrefix(d.Chat.ConversationKey, d.ID+":"))))) {
		return false
	}
	if d.Layout != nil {
		l := d.Layout
		if (l.PreviewDevice != "" && l.PreviewDevice != "mobile" && l.PreviewDevice != "tablet" && l.PreviewDevice != "desktop") || (l.FocusedPane != "" && l.FocusedPane != "chat" && l.FocusedPane != "preview") || (l.MobilePaneWidth != 0 && (l.MobilePaneWidth < 320 || l.MobilePaneWidth > 1600)) {
			return false
		}
		if (!l.ChatOpen && !l.PanelOpen) || l.SplitRatio < 0.1 || l.SplitRatio > 0.9 || (l.NavigationMode != "fixed" && l.NavigationMode != "auto-hide") {
			return false
		}
	}
	return true
}

func readWorkspaceReturnState(r *http.Request) (workspaceReturnState, error) {
	result := workspaceReturnState{Version: 1, Recent: []workspaceDestination{}}
	raw, exists, err := readFileFromWorkspace(r.Context(), workspaceReturnPath(GetUserIDFromContext(r.Context())))
	if err != nil {
		return result, err
	}
	if !exists {
		return result, nil
	}
	corrupt := func() (workspaceReturnState, error) {
		log.Printf("[WORKSPACE_STATE] user=%s invalid saved preferences; opening Home", GetUserIDFromContext(r.Context()))
		return workspaceReturnState{Version: 1, Recent: []workspaceDestination{}, Note: "Your saved workspace could not be read. Choose another item from Home."}, nil
	}
	if len(raw) > 32768 {
		return corrupt()
	}
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return corrupt()
	}
	if result.Version != 1 || len(result.Recent) > 20 {
		return corrupt()
	}
	result.Note = ""
	return result, nil
}

// Every restore rechecks resource visibility, independently of whether the
// caller can still read a former chat. Losing a share must close the project.
func (api *StreamingAPI) validateWorkspaceReturn(r *http.Request, d *workspaceDestination, work []homeWork) (*workspaceDestination, bool) {
	if d == nil {
		return nil, true
	}
	claims := GetUserFromContext(r.Context())
	if !validWorkspaceDestination(d) || !homeProductAllowed(claims, d.Surface) {
		return nil, false
	}
	copy := *d
	var workspace string
	if d.ID != "" {
		for _, item := range work {
			if item.Surface == d.Surface && item.ID == d.ID {
				workspace = item.WorkspacePath
				break
			}
		}
		if workspace == "" {
			return nil, false
		}
	}
	copy.WorkspacePath = workspace
	if copy.SessionID != "" {
		allowed := false
		session, active := api.getActiveSession(copy.SessionID)
		if active && session.UserID == claims.UserID && workspace != "" {
			allowed = canonicalChatHistoryWorkspacePath(claims.UserID, session.WorkspacePath) == canonicalChatHistoryWorkspacePath(claims.UserID, workspace)
		}
		if !active && workspace != "" {
			data, err := ReadChatHistoryConversation(claims.UserID, copy.SessionID, workspace)
			if err == nil {
				owner, _ := chatHistoryConversationIdentity(data)
				var history ChatHistorySession
				_ = json.Unmarshal(data, &history)
				runtime, runtimeErr := chatHistoryRuntimeFromJSON(data)
				visible := chatHistoryVisibleTo(owner, claims.UserID, false) || ((copy.Surface == "agentworks" || copy.Surface == "relays") && isWorkflowBotHistory(history))
				allowed = visible && runtimeErr == nil && runtime != nil && canonicalChatHistoryWorkspacePath(claims.UserID, runtime.WorkspacePath) == canonicalChatHistoryWorkspacePath(claims.UserID, workspace)
			}
		}
		if !allowed {
			copy.SessionID = ""
			copy.Chat = nil
		}
	}
	return &copy, true
}

func (api *StreamingAPI) handleWorkspaceReturnState(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	if claims == nil || claims.UserID == "" {
		http.Error(w, "unauthenticated", 401)
		return
	}
	work, err := api.homeInventory(r.Context())
	if err != nil {
		http.Error(w, "Could not validate your saved workspace.", 503)
		return
	}
	workspaceReturnMu.Lock()
	defer workspaceReturnMu.Unlock()
	state, err := readWorkspaceReturnState(r)
	if err != nil {
		http.Error(w, "Could not load your saved workspace.", 503)
		return
	}
	if r.Method == http.MethodPut {
		var incoming struct {
			Version  int                   `json:"version"`
			Revision uint64                `json:"revision"`
			Last     *workspaceDestination `json:"last"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&incoming); err != nil || incoming.Version != 1 || incoming.Last == nil || !validWorkspaceDestination(incoming.Last) {
			http.Error(w, "Invalid workspace state.", 400)
			return
		}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			http.Error(w, "Invalid workspace state.", 400)
			return
		}
		if incoming.Revision != state.Revision {
			http.Error(w, "Workspace changed in another tab. Reload its state before saving.", 409)
			return
		}
		destination, available := api.validateWorkspaceReturn(r, incoming.Last, work)
		if !available {
			http.Error(w, "This workspace is no longer available.", 403)
			return
		}
		state.Last = destination
		state.Revision++
		state.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
		recent := []workspaceDestination{*destination}
		for _, old := range state.Recent {
			if old.Surface != destination.Surface || old.ID != destination.ID {
				recent = append(recent, old)
			}
			if len(recent) == 20 {
				break
			}
		}
		state.Recent = recent
		state.Note = ""
		raw, err := json.Marshal(state)
		if err != nil {
			http.Error(w, "Could not encode workspace state.", 500)
			return
		}
		if err := writeRawFileToWorkspace(r.Context(), workspaceReturnPath(claims.UserID), string(raw)); err != nil {
			http.Error(w, "Could not save your workspace.", 503)
			return
		}
	}
	var available bool
	state.Last, available = api.validateWorkspaceReturn(r, state.Last, work)
	if !available {
		state.Note = "Your last workspace is no longer available. Choose another item from Home."
	}
	recent := []workspaceDestination{}
	for i := range state.Recent {
		if d, ok := api.validateWorkspaceReturn(r, &state.Recent[i], work); ok && d != nil {
			recent = append(recent, *d)
		}
	}
	state.Recent = recent
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(state)
}
