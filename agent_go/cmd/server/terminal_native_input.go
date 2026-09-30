package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/terminals"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	unifiedevents "github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/multi-llm-provider-go/pkg/tmuxinput"
)

// One observer belongs to the provider process, not its browser viewer. It
// survives view switches and resize reconnects, and stops when tmux exits.
type nativeTerminalObserver struct {
	api                                      *StreamingAPI
	snapshot                                 terminals.Snapshot
	ready                                    chan struct{}
	mu                                       sync.Mutex
	lastAssistant                            string
	nativeInputs                             bool
	sessionOwnsCompletion                    bool
	primed                                   bool
	initialSubmissions                       uint64
	fileStamp                                string
	homeResolved                             bool
	cachedNativeID                           string
	runtime                                  claudeNativeTranscriptRuntime
	home                                     string
	provider, nativeID, workingDir, path     string
	users                                    []string
	nativeCounts, coveredCounts, eventCounts map[string]int
	previous                                 []builderConversationMessage
	lastRead                                 time.Time
}

func (api *StreamingAPI) ensureNativeTerminalObserver(snapshot terminals.Snapshot) {
	if api.liveAttach == nil || api.eventStore == nil {
		return
	}
	m := api.liveAttach
	m.mu.Lock()
	if m.nativeObservers == nil {
		m.nativeObservers = map[string]*nativeTerminalObserver{}
	}
	if existing := m.nativeObservers[snapshot.TmuxSession]; existing != nil {
		m.mu.Unlock()
		<-existing.ready
		return
	}
	o := &nativeTerminalObserver{api: api, snapshot: snapshot, ready: make(chan struct{}),
		nativeCounts: map[string]int{}, coveredCounts: map[string]int{}, eventCounts: map[string]int{}}
	m.nativeObservers[snapshot.TmuxSession] = o
	m.mu.Unlock()
	o.poll(true)
	close(o.ready)
	go o.run()
}

func (o *nativeTerminalObserver) run() {
	ticker := time.NewTicker(400 * time.Millisecond)
	defer ticker.Stop()
	defer func() {
		o.api.liveAttach.mu.Lock()
		if o.api.liveAttach.nativeObservers[o.snapshot.TmuxSession] == o {
			delete(o.api.liveAttach.nativeObservers, o.snapshot.TmuxSession)
		}
		o.api.liveAttach.mu.Unlock()
		tmuxinput.Default.ClearInteractiveDraft(o.snapshot.TmuxSession)
	}()
	var checkedAt time.Time
	misses := 0
	for range ticker.C {
		if time.Since(checkedAt) >= 3*time.Second {
			ctx, cancel := context.WithTimeout(context.Background(), terminalTmuxActionTimeout)
			err := runTerminalTmuxCommand(ctx, "", "has-session", "-t", o.snapshot.TmuxSession)
			cancel()
			checkedAt = time.Now()
			if err != nil {
				misses++
			} else {
				misses = 0
			}
			if misses >= 3 {
				return
			}
		}
		o.poll(false)
	}
}

func (o *nativeTerminalObserver) read() ([]builderConversationMessage, string, bool) {
	// The live Session handle wins over persisted runtime, including native
	// session replacements. A cold server can observe its owned saved runtime.
	owner := o.api.eventStore.GetSessionOwner(o.snapshot.SessionID)
	o.api.sessionWorkspaceMu.RLock()
	workspace := o.api.sessionWorkspaceFolders[o.snapshot.SessionID]
	o.api.sessionWorkspaceMu.RUnlock()
	if session, ok := mcpagent.LookupSession(o.snapshot.SessionID); ok {
		h := session.Snapshot()
		if h != nil && (h.Provider.TmuxSession == "" || h.Provider.TmuxSession == o.snapshot.TmuxSession) {
			o.provider, o.nativeID, o.workingDir = string(h.Provider.Provider), h.Provider.NativeSessionID, h.Provider.WorkingDir
			if !o.homeResolved && h.ConnectionID != "" && owner != "" {
				if keys, err := o.api.connectionAPIKeys(context.Background(), providerAccountScope{Principal: owner, WorkspacePath: workspace}, o.provider, h.ConnectionID); err == nil {
					o.home = keys.RuntimeEnvironment["HOME"]
					o.homeResolved = true
				}
			}
		}
	}
	if o.nativeID == "" && owner != "" {
		raw, err := ReadChatHistoryConversation(owner, o.snapshot.SessionID, workspace)
		if err == nil {
			var record struct {
				UserID    string                        `json:"user_id"`
				SessionID string                        `json:"session_id"`
				Runtime   claudeNativeTranscriptRuntime `json:"runtime"`
			}
			if json.Unmarshal(raw, &record) == nil && record.UserID == owner && record.SessionID == o.snapshot.SessionID {
				o.runtime = record.Runtime
				o.provider, o.nativeID = record.Runtime.Provider, record.Runtime.ExternalSessionID
				if h := record.Runtime.AgentSessionHandle; h != nil && h.Provider != nil {
					if o.provider == "" {
						o.provider = h.Provider.Provider
					}
					if o.nativeID == "" {
						o.nativeID = h.Provider.NativeSessionID
					}
					o.workingDir = h.Provider.WorkingDir
					if h.ConnectionID != "" {
						if keys, err := o.api.connectionAPIKeys(context.Background(), providerAccountScope{Principal: owner, WorkspacePath: workspace}, o.provider, h.ConnectionID); err == nil {
							o.home = keys.RuntimeEnvironment["HOME"]
						}
					}
				}
			}
		}
	}
	if o.nativeID == "" {
		return nil, "", false
	}
	var beforeStamp string
	if o.path != "" && o.cachedNativeID == o.nativeID {
		stamp := nativeTerminalFileStamp(o.path)
		beforeStamp = stamp
		if stamp != "" && stamp == o.fileStamp {
			return nil, o.path, false
		}
	}
	messages, _, path, ok, err := nativeTranscriptMessagesForRuntimeUncapped(o.provider, o.nativeID, o.workingDir, o.home)
	if ok && err == nil {
		afterStamp := nativeTerminalFileStamp(path)
		o.fileStamp = ""
		if path == o.path && beforeStamp == afterStamp {
			o.fileStamp = afterStamp
		}
		o.cachedNativeID = o.nativeID
	}
	return messages, path, ok && err == nil
}

// SQLite writers can change WAL without touching the main database. Compare
// both before reparsing a complete history; idle sessions cost only stat calls.
func nativeTerminalFileStamp(path string) string {
	var stamp strings.Builder
	for _, suffix := range []string{"", "-wal"} {
		if info, err := os.Stat(path + suffix); err == nil {
			fmt.Fprintf(&stamp, "%s:%d:%d;", suffix, info.Size(), info.ModTime().UnixNano())
		}
	}
	return stamp.String()
}

func (o *nativeTerminalObserver) poll(prime bool) {
	messages, path, ok := o.read()
	if !ok {
		if prime {
			o.primed, o.lastRead = true, time.Now()
			o.initialSubmissions = tmuxinput.Default.InteractiveSubmissionCount(o.snapshot.TmuxSession)
			o.eventCounts = nativeTerminalEventUserCounts(o.api.eventStore, o.snapshot.SessionID)
			for text, count := range o.eventCounts {
				o.coveredCounts[text] = count
			}
		}
		return
	} // A missing/partially flushed transcript never advances the cursor.
	o.consume(messages, path, prime)
}

func (o *nativeTerminalObserver) consume(messages []builderConversationMessage, path string, prime bool) {
	now := time.Now()
	users := nativeTerminalUsers(messages)
	counts := nativeTerminalEventUserCounts(o.api.eventStore, o.snapshot.SessionID)
	if prime || !o.primed || (o.path != "" && o.path != path) {
		o.primed = true
		o.initialSubmissions = tmuxinput.Default.InteractiveSubmissionCount(o.snapshot.TmuxSession)
		o.path, o.users, o.previous, o.lastRead = path, users, messages, now
		o.nativeCounts = map[string]int{}
		for _, text := range users {
			o.nativeCounts[text]++
		}
		o.coveredCounts = map[string]int{}
		for text, count := range o.nativeCounts {
			o.coveredCounts[text] = count
		}
		for text, count := range counts {
			if count > o.coveredCounts[text] {
				o.coveredCounts[text] = count
			}
		}
		o.eventCounts = counts
		return
	}
	// An API send can be waiting in EventStore's deferred-user hold. Count it
	// before considering native rows, so the same accepted prompt has one bubble.
	for text, count := range counts {
		if delta := count - o.eventCounts[text]; delta > 0 {
			o.coveredCounts[text] += delta
		}
	}
	newUsers := nativeTerminalNewUsers(o.users, users)
	if o.path == "" {
		// A log can appear after attach. Only Enter attempts made since the
		// baseline can belong to this viewer; older recovered history is baseline.
		attempts := int(tmuxinput.Default.InteractiveSubmissionCount(o.snapshot.TmuxSession) - o.initialSubmissions)
		if len(newUsers) > attempts {
			for _, text := range newUsers[:len(newUsers)-attempts] {
				o.nativeCounts[text]++
				if o.coveredCounts[text] < o.nativeCounts[text] {
					o.coveredCounts[text] = o.nativeCounts[text]
				}
			}
			newUsers = newUsers[len(newUsers)-attempts:]
		}
	}
	for _, text := range newUsers {
		o.nativeCounts[text]++
		if o.nativeCounts[text] <= o.coveredCounts[text] {
			continue
		}
		// User rows first observed without terminal input belong to the normal
		// Chat path. It owns their lifecycle and will publish its own user event.
		if !tmuxinput.Default.HasInteractiveSubmissions(o.snapshot.TmuxSession) && !o.hasNativeInputs() {
			o.coveredCounts[text]++
			continue
		}
		o.coveredCounts[text]++
		acceptedAt := nativeTerminalPromptTime(path, o.provider, text, o.lastRead)
		o.accept(text, acceptedAt)
	}
	if o.hasNativeInputs() {
		o.mu.Lock()
		ownsCompletion := o.sessionOwnsCompletion
		o.mu.Unlock()
		_, warm := mcpagent.LookupSession(o.snapshot.SessionID)
		if !ownsCompletion || !warm {
			o.api.publishNativeTranscriptRecoveredAssistantMessages(o.snapshot.SessionID, o.previous, messages, nil)
		}
		o.mu.Lock()
		for i := len(messages) - 1; i >= 0; i-- {
			if builderConversationRoleIsAssistant(messages[i].Role) {
				o.lastAssistant = builderConversationMessageText(messages[i])
				break
			}
			if messages[i].Role == "human" {
				break
			}
		}
		o.mu.Unlock()
	}
	o.path, o.users, o.previous, o.lastRead = path, users, messages, now
	o.eventCounts = nativeTerminalEventUserCounts(o.api.eventStore, o.snapshot.SessionID)
}

func (o *nativeTerminalObserver) hasNativeInputs() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.nativeInputs
}

func (o *nativeTerminalObserver) accept(text string, acceptedAt time.Time) {
	key := sha256.Sum256([]byte(fmt.Sprintf("%s:%s:%d:%s", o.snapshot.SessionID, o.nativeID, o.nativeCounts[text], text)))
	id := fmt.Sprintf("user-message-native-%x", key[:12])
	event := unifiedevents.NewUserMessageEvent(0, text, "user")
	event.Timestamp = acceptedAt
	event.Metadata = map[string]interface{}{"source": "native_terminal", "provider": o.provider, "client_message_id": id, "message_id": id, "delivery_status": "sent_to_cli"}
	wrapped := unifiedevents.NewAgentEvent(event)
	wrapped.SessionID = o.snapshot.SessionID
	wrapped.Component = "coding_agent_native_input"
	o.api.eventStore.AddEvent(o.snapshot.SessionID, storeevents.Event{ID: id, Type: "user_message", Timestamp: acceptedAt, SessionID: o.snapshot.SessionID, ExecutionKind: "main_agent", TerminalOwnerID: "main:" + o.snapshot.SessionID, Data: wrapped})
	o.mu.Lock()
	o.nativeInputs, o.lastAssistant = true, ""
	o.mu.Unlock()
	tmuxinput.Default.ConfirmInteractiveSubmission(o.snapshot.TmuxSession)
	if session, ok := mcpagent.LookupSession(o.snapshot.SessionID); ok {
		turnID, err := session.ObserveNativeInput(context.Background(), text, acceptedAt)
		if err == nil {
			o.mu.Lock()
			o.sessionOwnsCompletion = true
			o.mu.Unlock()
			o.api.markMCPAgentSessionTurnRunning(o.snapshot.SessionID, turnID)
		} else {
			o.mu.Lock()
			o.sessionOwnsCompletion = false
			o.mu.Unlock()
			log.Printf("[NATIVE_TERMINAL] Cannot adopt accepted input session=%s provider=%s: %v", o.snapshot.SessionID, o.provider, err)
			o.api.markRetainedMainCodingTurnRunning(o.snapshot.SessionID, id)
		}
	} else {
		o.api.markRetainedMainCodingTurnRunning(o.snapshot.SessionID, id)
	}
	start := unifiedevents.NewAgentEvent(&unifiedevents.StreamingStartEvent{BaseEventData: unifiedevents.BaseEventData{Timestamp: acceptedAt}, Provider: o.provider})
	start.SessionID = o.snapshot.SessionID
	o.api.eventStore.AddEvent(o.snapshot.SessionID, storeevents.Event{ID: id + ":start", Type: "streaming_start", Timestamp: acceptedAt, SessionID: o.snapshot.SessionID, Data: start})
	owner := o.api.eventStore.GetSessionOwner(o.snapshot.SessionID)
	o.api.sessionWorkspaceMu.RLock()
	workspace := o.api.sessionWorkspaceFolders[o.snapshot.SessionID]
	o.api.sessionWorkspaceMu.RUnlock()
	if owner != "" {
		o.api.appendLiveInputToPersistedChatHistory(owner, o.snapshot.SessionID, workspace, text)
	}
}

func nativeTerminalUsers(messages []builderConversationMessage) []string {
	var out []string
	for _, message := range messages {
		if message.Role == "human" || message.Role == "user" {
			if text := strings.TrimSpace(builderConversationMessageText(message)); text != "" {
				out = append(out, text)
			}
		}
	}
	return out
}

// Match the longest retained suffix when a capped transcript drops its prefix.
// Prefix equality preserves intentional repetitions rather than text-deduping.
func nativeTerminalNewUsers(previous, current []string) []string {
	for overlap := min(len(previous), len(current)); overlap >= 0; overlap-- {
		match := true
		for i := 0; i < overlap; i++ {
			if previous[len(previous)-overlap+i] != current[i] {
				match = false
				break
			}
		}
		if match {
			return current[overlap:]
		}
	}
	return nil
}

func nativeTerminalEventUserCounts(store *storeevents.EventStore, sessionID string) map[string]int {
	counts := map[string]int{}
	seen := map[string]bool{}
	rows := append(store.GetAllEventsRaw(sessionID), store.PendingUserMessages(sessionID)...)
	for _, row := range rows {
		if row.Type != "user_message" || seen[row.ID] {
			continue
		}
		seen[row.ID] = true
		if text, ok := eventPayloadMap(row)["content"].(string); ok {
			counts[strings.TrimSpace(text)]++
		}
	}
	return counts
}

// JSONL providers have real acceptance timestamps. A previous poll time is the
// conservative boundary for SQLite providers, whose adapter pins native refs.
func nativeTerminalPromptTime(path, provider, text string, fallback time.Time) time.Time {
	if strings.HasSuffix(path, ".db") {
		return fallback
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return fallback
	}
	matched := fallback
	for _, line := range strings.Split(string(raw), "\n") {
		var row map[string]json.RawMessage
		if json.Unmarshal([]byte(line), &row) != nil {
			continue
		}
		var msg struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
			Type    string          `json:"type"`
		}
		encoded := row["message"]
		if provider == "codex-cli" {
			encoded = row["payload"]
		}
		if json.Unmarshal(encoded, &msg) != nil {
			continue
		}
		var plain string
		_ = json.Unmarshal(msg.Content, &plain)
		if plain == "" {
			var blocks []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}
			_ = json.Unmarshal(msg.Content, &blocks)
			var texts []string
			for _, block := range blocks {
				if block.Type == "text" || block.Type == "input_text" {
					texts = append(texts, block.Text)
				}
			}
			plain = strings.Join(texts, "\n\n")
		}
		var kind string
		_ = json.Unmarshal(row["type"], &kind)
		if msg.Role != "user" && kind != "user" {
			continue
		}
		if strings.Join(strings.Fields(plain), " ") != strings.Join(strings.Fields(text), " ") {
			continue
		}
		var timestamp string
		_ = json.Unmarshal(row["timestamp"], &timestamp)
		if at, err := time.Parse(time.RFC3339Nano, timestamp); err == nil {
			matched = at
		}
	}
	return matched
}

func (api *StreamingAPI) nativeTerminalFinalResponse(tmuxSession string) string {
	if api.liveAttach == nil {
		return ""
	}
	api.liveAttach.mu.Lock()
	o := api.liveAttach.nativeObservers[tmuxSession]
	api.liveAttach.mu.Unlock()
	if o == nil {
		return ""
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.lastAssistant
}

func (api *StreamingAPI) hasNativeTerminalDraft(sessionID string) bool {
	if api.terminalStore == nil {
		return false
	}
	for _, snapshot := range api.terminalStore.ListRaw(sessionID) {
		if terminalSnapshotIsMainAgent(snapshot) && tmuxinput.Default.HasInteractiveDraft(snapshot.TmuxSession) {
			return true
		}
	}
	return false
}
