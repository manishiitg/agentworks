package server

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
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
	Name             string `json:"name,omitempty"`
	ReadOnly         bool   `json:"read_only,omitempty"`
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
	Version     int                    `json:"version"`
	Revision    uint64                 `json:"revision"`
	Last        *workspaceDestination  `json:"last"`
	Recent      []workspaceDestination `json:"recent"`
	UpdatedAt   string                 `json:"updated_at,omitempty"`
	Note        string                 `json:"note,omitempty"`        // response only
	Destination *workspaceDestination  `json:"destination,omitempty"` // response only
	FirstVisit  bool                   `json:"first_visit,omitempty"` // response only
}

var workspaceReturnLocks workspaceReturnLockSet
var workspaceReturnID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.:-]{0,255}$`)
var workspaceReturnView = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

// Waiters hold a reference before waiting, so an idle key can be removed
// without letting a later caller acquire a different lock for the same key.
type workspaceReturnLockSet struct {
	mu    sync.Mutex
	locks map[string]*workspaceReturnLock
}

type workspaceReturnLock struct {
	mu   sync.Mutex
	refs int
}

func (s *workspaceReturnLockSet) Lock(key string) func() {
	s.mu.Lock()
	if s.locks == nil {
		s.locks = map[string]*workspaceReturnLock{}
	}
	lock := s.locks[key]
	if lock == nil {
		lock = &workspaceReturnLock{}
		s.locks[key] = lock
	}
	lock.refs++
	s.mu.Unlock()
	lock.mu.Lock()
	return func() {
		lock.mu.Unlock()
		s.mu.Lock()
		lock.refs--
		if lock.refs == 0 {
			delete(s.locks, key)
		}
		s.mu.Unlock()
	}
}

func workspaceReturnPath(user string) string {
	return filepath.ToSlash(filepath.Join(workspaceref.PhysicalPath(user, "preferences"), "workspace-state.json"))
}
func validWorkspaceDestination(d *workspaceDestination) bool {
	if d == nil {
		return true
	}
	known := map[string]bool{"agentworks": true, "relays": true, "work": true, "mcp-gateway": true, "llm-gateway": true, "knowledgebase": true, "bullpen": true, "sparkquill": true}
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
		log.Printf("[WORKSPACE_STATE] user=%s invalid saved preferences; opening an available workspace", GetUserIDFromContext(r.Context()))
		return workspaceReturnState{Version: 1, Recent: []workspaceDestination{}, Note: "Your saved workspace could not be read. Choose another workspace."}, nil
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
	result.Destination = nil
	result.FirstVisit = false
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
	// Browser preferences cannot assert a conversation's authority or mode.
	personalView, personalName := "", ""
	if copy.Chat != nil {
		personalView, personalName = copy.Chat.ViewMode, copy.Chat.Name
	}
	copy.Chat = nil
	if copy.SessionID != "" {
		allowed := false
		historyMissing := false
		historyWithoutRuntime := false
		currentConversation := false
		var history ChatHistorySession
		var trigger, title, mode, platform string
		session, active := api.getActiveSession(copy.SessionID)
		if active && session.UserID == claims.UserID && workspace != "" && session.ParentSessionID == "" {
			allowed = canonicalChatHistoryWorkspacePath(claims.UserID, session.WorkspacePath) == canonicalChatHistoryWorkspacePath(claims.UserID, workspace)
			title, mode, platform, trigger = session.Title, session.WorkshopMode, session.BotPlatform, session.TriggeredBy
		}
		if !active && workspace != "" {
			data, err := ReadChatHistoryConversation(claims.UserID, copy.SessionID, workspace)
			historyMissing = errors.Is(err, errChatHistoryConversationNotFound)
			if err == nil {
				owner, _ := chatHistoryConversationIdentity(data)
				_ = json.Unmarshal(data, &history)
				runtime, runtimeErr := chatHistoryRuntimeFromJSON(data)
				visible := chatHistoryVisibleTo(owner, claims.UserID, false) || ((copy.Surface == "agentworks" || copy.Surface == "relays") && isWorkflowBotHistory(history))
				allowed = visible && runtimeErr == nil && runtime != nil && canonicalChatHistoryWorkspacePath(claims.UserID, runtime.WorkspacePath) == canonicalChatHistoryWorkspacePath(claims.UserID, workspace)
				historyWithoutRuntime = visible && runtimeErr == nil && runtime == nil
				history.CanResume = chatHistoryCanResume(owner, claims.UserID, false)
				title, mode, platform = history.Title, history.WorkshopMode, history.BotPlatform
				if runtime != nil {
					mode = firstNonEmptyTrimmed(mode, runtime.WorkshopMode)
				}
			}
		}
		conversationKey := ""
		if copy.Surface == "work" {
			registryPath := productConversationRegistryPath(claims.UserID)
			mutex := productConversationRegistryMutex(registryPath)
			mutex.Lock()
			document, err := defaultProductConversationRegistryStore().loadDocument(r.Context(), registryPath)
			mutex.Unlock()
			if err == nil {
				match := func(record ProductConversationRecord, current bool) {
					key := record.ConversationKey
					if record.ProfileID == copy.Surface && record.SessionID == copy.SessionID && (key == copy.ID || strings.HasPrefix(key, copy.ID+":")) && canonicalChatHistoryWorkspacePath(claims.UserID, record.WorkspacePath) == canonicalChatHistoryWorkspacePath(claims.UserID, workspace) {
						conversationKey = key
						currentConversation = current
					}
				}
				for _, record := range document.Entries {
					match(record, true)
				}
				if conversationKey == "" {
					for _, records := range document.Previous {
						for _, record := range records {
							match(record, false)
						}
					}
				}
			}
		}
		// A current registry-owned chat can be empty or have an imported archive
		// before its first native turn. Foreign and mismatched histories stay denied.
		if !active && (historyMissing || historyWithoutRuntime) && currentConversation && conversationKey != "" {
			allowed = true
			history.CanResume = true
		}
		if !allowed {
			copy.SessionID = ""
		} else {
			id := strings.ToLower(copy.SessionID)
			scheduled := strings.HasPrefix(id, "schedule-") || strings.HasPrefix(id, "sched_") || strings.Contains(id, "-schedule-") || strings.Contains(trigger, "schedule") || trigger == "cron" || trigger == "webhook"
			bot := platform != "" || strings.HasPrefix(id, "bot-")
			execution := (copy.Surface == "agentworks" || copy.Surface == "relays") && (scheduled || bot)
			normalized := normalizeChatHistoryWorkshopMode(mode)
			if normalized == "" && (copy.Surface == "agentworks" || copy.Surface == "relays") {
				normalized = "workshop"
			}
			copy.Chat = &workspaceChat{Name: firstNonEmptyTrimmed(personalName, title, "Chat"), ReadOnly: scheduled || bot || execution || !active && !history.CanResume && history.SessionID != "", ExecutionRun: execution, ScheduledRun: scheduled, BotPlatform: platform, WorkshopMode: normalized, ViewMode: personalView}
			if scheduled {
				copy.Chat.ScheduledJobName = firstNonEmptyTrimmed(title, "Scheduled run")
			}
			copy.Chat.ConversationKey = conversationKey
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
	unlock := workspaceReturnLocks.Lock(claims.UserID)
	defer unlock()
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
		// Returning to work is a per-user preference: the last completed save
		// wins across browser tabs. A stale revision never changes authority;
		// the selected project is still checked through the live resolver.
		// Saves are preferences, not authorization. Validate the single project
		// with its live resolver; never scan inventory or reopen transcripts.
		var work []homeWork
		if incoming.Last.ID != "" {
			kind := map[string]string{"work": "work", "agentworks": "workflow", "relays": "relay"}[incoming.Last.Surface]
			target, err := api.projectMCPTarget(r.Context(), claims, projectMCPAudience{Kind: kind, ID: incoming.Last.ID, Access: "view"})
			if err != nil || kind == "" {
				causeStatus := wf.StatusCode(err)
				status := http.StatusForbidden
				message := "This workspace is no longer available."
				// A temporary discovery/service failure cannot establish that a
				// project was revoked. Keep the saved preference and ask for retry;
				// missing and denied projects still share the same refusal below.
				if kind != "" && (causeStatus >= 500 || r.Context().Err() != nil) {
					status = http.StatusServiceUnavailable
					message = "Could not check your workspace access. Try again shortly."
				}
				log.Printf("[WORKSPACE_STATE] access check refused user=%q surface=%q id=%q status=%d cause_status=%d context_error=%v", claims.UserID, incoming.Last.Surface, incoming.Last.ID, status, causeStatus, r.Context().Err())
				http.Error(w, message, status)
				return
			}
			work = []homeWork{{Surface: incoming.Last.Surface, ID: incoming.Last.ID, WorkspacePath: target.Root}}
		}
		saved := *incoming.Last
		saved.SessionID = "" // session identity is checked on restore, below; no history reads on PUT
		destination, available := api.validateWorkspaceReturn(r, &saved, work)
		if available {
			destination.SessionID = incoming.Last.SessionID
			if incoming.Last.Chat != nil {
				destination.Chat = &workspaceChat{Name: incoming.Last.Chat.Name, ViewMode: incoming.Last.Chat.ViewMode}
			}
		}
		if !available {
			reason := "workspace not found"
			if !homeProductAllowed(claims, incoming.Last.Surface) {
				reason = "product not enabled on this server or for this account"
			}
			log.Printf("[WORKSPACE_STATE] restore check refused user=%q surface=%q id=%q reason=%q", claims.UserID, incoming.Last.Surface, incoming.Last.ID, reason)
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
		state.Destination = nil
		state.FirstVisit = false
		raw, err := json.Marshal(state)
		if err != nil {
			http.Error(w, "Could not encode workspace state.", 500)
			return
		}
		if err := writeRawFileToWorkspace(r.Context(), workspaceReturnPath(claims.UserID), string(raw)); err != nil {
			http.Error(w, "Could not save your workspace.", 503)
			return
		}
		// A save acknowledgement contains layout only. Session identity and
		// modes are restore data only after GET has checked the live session.
		state.Last.SessionID = ""
		state.Last.Chat = nil
		for i := range state.Recent {
			state.Recent[i].SessionID = ""
			state.Recent[i].Chat = nil
		}
	}
	if r.Method == http.MethodGet {
		// Only restore performs inventory/history authorization. Recent entries
		// are project-checked without loading up to twenty old transcripts.
		work, err := api.homeInventory(r.Context())
		if err != nil {
			http.Error(w, "Could not validate your saved workspace.", 503)
			return
		}
		var available bool
		state.Last, available = api.validateWorkspaceReturn(r, state.Last, work)
		if !available {
			state.Note = "Your last workspace is no longer available. Choose another workspace."
		}
		recent := []workspaceDestination{}
		for _, old := range state.Recent {
			old.SessionID = ""
			old.Chat = nil
			if d, ok := api.validateWorkspaceReturn(r, &old, work); ok && d != nil {
				recent = append(recent, *d)
			}
		}
		state.Recent = recent
		state.Destination = state.Last
		if state.Destination == nil {
			sort.SliceStable(work, func(i, j int) bool { return work[i].UpdatedAt > work[j].UpdatedAt })
			if len(recent) > 0 {
				state.Destination = &recent[0]
			} else if len(work) > 0 {
				state.Destination = &workspaceDestination{Surface: work[0].Surface, ID: work[0].ID, WorkspacePath: work[0].WorkspacePath}
			} else {
				preferred := strings.TrimSpace(os.Getenv("AGENTWORKS_DEFAULT_PRODUCT_SURFACE"))
				for _, surface := range append([]string{preferred}, "agentworks", "work", "relays", "knowledgebase", "mcp-gateway", "llm-gateway", "bullpen", "sparkquill") {
					if surface != "" && homeProductAllowed(claims, surface) {
						state.Destination = &workspaceDestination{Surface: surface}
						break
					}
				}
				state.FirstVisit = state.Note == ""
			}
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(state)
}

// App-only capability projection. The project resolver owns both account and
// project authority; a browser storage path is never an editing grant.
func (api *StreamingAPI) handleWorkspaceAccess(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	if claims == nil || claims.AccessToken != nil || claims.Scope != "" {
		externalError(w, 403, "forbidden", "Sign in to open this project.")
		return
	}
	target, err := api.dashboardTarget(r.Context(), claims, r.URL.Query().Get("workspace"))
	if err != nil {
		dashboardError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	externalJSON(w, map[string]any{"id": target.ID, "kind": target.Kind, "can_build": target.Edit})
}
