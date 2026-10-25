package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/terminals"
	"github.com/manishiitg/coding-agent-loop/workspace/security"
	mcpagent "github.com/manishiitg/mcpagent/agent"
	agentevents "github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/cursorcli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/musecli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/picli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/nativeevents"
	"github.com/manishiitg/multi-llm-provider-go/pkg/nativereplies"
)

// This checkpoint belongs to an exact platform-owned native runtime. It
// contains identities and operational state, never chat text or tool payloads.
type nativeCodingRecordState struct {
	Provider   string                           `json:"provider"`
	NativeID   string                           `json:"native_session_id"`
	Path       string                           `json:"path"`
	Cursor     nativeevents.Cursor              `json:"cursor"`
	Primed     bool                             `json:"primed"`
	Helpers    map[string]llmtypes.NativeRecord `json:"helpers"`
	Turns      map[string]llmtypes.NativeRecord `json:"turns"`
	filename   string
	lastError  string
	stamp      string
	BaselineAt time.Time `json:"baseline_at"`
}

var errNativeForegroundPending = errors.New("native continuation owned by unfinished foreground")

func (api *StreamingAPI) observeNativeCodingRuntime(sessionID string, event storeevents.Event) {
	if api.eventStore == nil || !api.eventStore.IsDurableChatSession(sessionID) {
		return
	}
	session, ok := mcpagent.LookupSession(sessionID)
	if !ok {
		return
	}
	handle := session.Snapshot()
	if handle == nil || len(nativeevents.Capabilities(handle.Provider.Provider)) == 0 {
		return
	}
	snapshot := terminals.Snapshot{SessionID: sessionID, TmuxSession: handle.Provider.TmuxSession, ExecutionKind: "main_agent", CreatedAt: event.Timestamp}
	if api.terminalStore != nil {
		for _, current := range api.terminalStore.ListRaw(sessionID) {
			if codingAgentSnapshotIsMainAgent(current) {
				snapshot = current
				break
			}
		}
	}
	go api.ensureNativeTerminalObserver(snapshot)
}

func (o *nativeTerminalObserver) nativeRecordPath() string {
	if o.nativeID == "" || strings.ContainsAny(o.nativeID, `/\`) {
		return ""
	}
	home := o.home
	if filepath.IsAbs(o.workingDir) {
		private := filepath.Join(o.workingDir, security.SandboxPersistentDirName, "cli-home", cliHomeName(o.provider))
		if info, err := os.Stat(private); err == nil && info.IsDir() {
			home = private
		}
	}
	switch o.provider {
	case "claude-code":
		p, _ := resolveClaudeNativeTranscriptPath(o.workingDir, o.nativeID, home)
		return p
	case "codex-cli":
		p, _ := resolveCodexNativeTranscriptPath(o.nativeID, home)
		return p
	case "cursor-cli":
		return cursorcli.NativeRecordPath(o.workingDir, o.nativeID, home)
	case "pi-cli":
		return picli.NativeRecordPath(o.workingDir, o.nativeID)
	case "muse-cli":
		dataHome := ""
		if home != "" {
			dataHome = filepath.Join(home, ".local", "share")
		}
		return musecli.NativeRecordPath(o.nativeID, dataHome)
	case "agy-cli":
		if home == "" {
			home, _ = os.UserHomeDir()
		}
		return filepath.Join(home, ".gemini", "antigravity-cli", "conversations", o.nativeID+".db")
	}
	return ""
}

func nativeCodingEventID(sessionID string, r llmtypes.NativeRecord) string {
	h := sha256.Sum256([]byte(strings.Join([]string{sessionID, r.Provider, r.SessionID, r.ID, r.Kind, r.State}, "\x00")))
	return fmt.Sprintf("native-record-%x", h[:20])
}

func (o *nativeTerminalObserver) pollNativeCodingRecords() {
	path := o.nativeRecordPath()
	if path == "" {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	s := o.nativeRecords
	if s == nil || s.Provider != o.provider || s.NativeID != o.nativeID {
		root, err := workflowCLIStateRoot()
		if err != nil {
			return
		}
		key := sha256.Sum256([]byte(o.snapshot.SessionID + "\x00" + o.provider + "\x00" + o.nativeID))
		baseline := time.Now()
		if !o.snapshot.CreatedAt.IsZero() && time.Since(o.snapshot.CreatedAt) < 5*time.Second {
			baseline = o.snapshot.CreatedAt
		}
		s = &nativeCodingRecordState{Provider: o.provider, NativeID: o.nativeID, Path: path, BaselineAt: baseline, Helpers: map[string]llmtypes.NativeRecord{}, Turns: map[string]llmtypes.NativeRecord{}, filename: filepath.Join(root, "native-records", fmt.Sprintf("%x.json", key[:20]))}
		if raw, err := os.ReadFile(s.filename); err == nil {
			var saved nativeCodingRecordState
			if json.Unmarshal(raw, &saved) == nil && saved.Provider == s.Provider && saved.NativeID == s.NativeID && saved.Path == path {
				saved.filename = s.filename
				s = &saved
			}
		}
		if s.Helpers == nil {
			s.Helpers = map[string]llmtypes.NativeRecord{}
		}
		if s.Turns == nil {
			s.Turns = map[string]llmtypes.NativeRecord{}
		}
		o.nativeRecords = s
	}
	if path != s.Path {
		o.nativeCodingGap(s, "source_path_changed")
		return
	}
	stamp := nativeTerminalFileStamp(path)
	if stamp != "" && stamp == s.stamp {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	batch, err := nativeevents.Read(ctx, o.provider, o.nativeID, path, s.Cursor)
	cancel()
	if err != nil {
		if !os.IsNotExist(err) {
			o.nativeCodingGap(s, err.Error())
		}
		return
	}
	boundary := s.BaselineAt
	// No saved cursor means this is a baseline, not permission to import old
	// history. Timestamp-less baseline records supply identities only.
	for _, record := range batch.Records {
		if !s.Primed && (record.Timestamp.IsZero() || boundary.IsZero() || record.Timestamp.Before(boundary)) {
			continue
		}
		if err := o.acceptNativeCodingRecord(record); err != nil {
			if errors.Is(err, errNativeForegroundPending) {
				if e := saveNativeCodingState(s); e != nil {
					o.nativeCodingGap(s, "checkpoint_write_failed")
				}
				return
			}
			o.nativeCodingGap(s, "durable_accept_failed")
			return
		}
		record.Text = ""
		if record.Kind == "helper" && record.TaskID != "" {
			s.Helpers[record.TaskID] = record
		}
		if record.Kind == "turn" && record.Origin == "helper_finished" && record.TurnID != "" {
			s.Turns[record.TurnID] = record
		}
	}
	if len(batch.Unknown) > 0 || batch.Invalid > 0 || batch.Gaps > 0 {
		// Only schema labels and counts: never native payloads, account limits,
		// reasoning, prompts or replies in operational logs.
		unknown, _ := json.Marshal(batch.Unknown)
		log.Printf("[NATIVE_RECORD] coverage session=%s provider=%s native=%s decoder=%s unknown=%s invalid=%d identity_gaps=%d", o.snapshot.SessionID, o.provider, o.nativeID, nativeevents.DecoderVersion, unknown, batch.Invalid, batch.Gaps)
	}
	previousCursor, previousPrimed := s.Cursor, s.Primed
	s.Cursor = batch.Next
	// JSONL reads are bounded; do not label the first partial batch as a full
	// baseline. SQLite readers already consume the selected committed boundary.
	if batch.CaughtUp {
		s.Primed = true
	}
	if err := saveNativeCodingState(s); err != nil {
		s.Cursor, s.Primed = previousCursor, previousPrimed
		o.nativeCodingGap(s, "checkpoint_write_failed")
		return
	}
	if batch.CaughtUp {
		s.stamp = stamp
	}
	s.lastError = ""
}

func (o *nativeTerminalObserver) nativeCodingGap(s *nativeCodingRecordState, reason string) {
	if s.lastError == reason {
		return
	}
	s.lastError = reason
	// Reader errors are bounded diagnostics authored by our adapter. Filesystem
	// paths are not copied to the UI or to cross-server audit reports.
	log.Printf("[NATIVE_RECORD] gap session=%s provider=%s native=%s reason=%q", o.snapshot.SessionID, o.provider, o.nativeID, reason)
}

func saveNativeCodingState(s *nativeCodingRecordState) error {
	if err := os.MkdirAll(filepath.Dir(s.filename), 0700); err != nil {
		return err
	}
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	tmp := s.filename + ".tmp"
	if err = os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, s.filename)
}

func (o *nativeTerminalObserver) acceptNativeCodingRecord(record llmtypes.NativeRecord) error {
	id := nativeCodingEventID(o.snapshot.SessionID, record)
	observedAt := time.Now()
	activity := agentevents.NewCodingAgentNativeActivity(record)
	activity.EventID = id
	wrapped := agentevents.NewAgentEvent(activity)
	wrapped.SessionID = o.snapshot.SessionID
	event := storeevents.Event{ID: id, Type: string(agentevents.CodingAgentNativeActivity), Timestamp: observedAt, SessionID: o.snapshot.SessionID, ExecutionKind: "main_agent", Data: wrapped}
	if err := o.api.eventStore.AddEventChecked(o.snapshot.SessionID, event); err != nil {
		return err
	}
	log.Printf("[NATIVE_RECORD] accepted session=%s provider=%s native=%s turn=%s item=%s kind=%s state=%s followup=%s event=%s", o.snapshot.SessionID, record.Provider, record.SessionID, record.TurnID, record.ID, record.Kind, record.State, record.FollowupID, id)
	publicationOwner := ""
	if record.Origin == "helper_finished" && record.TurnID != "" {
		proposed := nativereplies.Observer
		if session, ok := mcpagent.LookupSession(o.snapshot.SessionID); ok && session.ActiveTurnID() != "" {
			proposed = nativereplies.Foreground
		}
		var err error
		publicationOwner, err = nativereplies.Claim(record.Provider, record.SessionID, record.TurnID, proposed)
		if err != nil {
			return err
		}
	}
	// Exact lineage decides ownership, not elapsed time or the current SDK
	// state. A queued transcript-only notification has no native turn root.
	if record.Kind != "reply" || record.Origin != "helper_finished" || record.TurnID == "" || strings.TrimSpace(record.Text) == "" {
		return nil
	}
	if publicationOwner == nativereplies.Foreground {
		if !nativereplies.State(record.Provider, record.SessionID, record.TurnID).Committed {
			return errNativeForegroundPending
		}
		return nil
	}
	chunk := &agentevents.StreamingChunkEvent{BaseEventData: agentevents.BaseEventData{EventID: id + ":reply", Timestamp: observedAt, Metadata: map[string]interface{}{"native_continuation": true, "provider": record.Provider, "native_record_id": record.ID, "native_turn_id": record.TurnID}}, Source: "transcript", Content: record.Text}
	reply := storeevents.Event{ID: id + ":reply", Type: "streaming_chunk", Timestamp: observedAt, SessionID: o.snapshot.SessionID, ExecutionKind: "main_agent", Data: agentevents.NewAgentEvent(chunk)}
	reply.Data.SessionID = o.snapshot.SessionID
	reply.Data.TurnID = "native:" + record.TurnID
	if err := o.api.eventStore.AddEventChecked(o.snapshot.SessionID, reply); err != nil {
		return err
	}
	owner := o.api.eventStore.GetSessionOwner(o.snapshot.SessionID)
	o.api.sessionWorkspaceMu.RLock()
	workspace := o.api.sessionWorkspaceFolders[o.snapshot.SessionID]
	o.api.sessionWorkspaceMu.RUnlock()
	if !o.api.appendStructuredAssistantReply(owner, o.snapshot.SessionID, workspace, id+":reply", record.Text, reply) {
		return fmt.Errorf("native reply history checkpoint not accepted")
	}
	log.Printf("[NATIVE_RECORD] reply_saved session=%s provider=%s native=%s turn=%s item=%s event=%s", o.snapshot.SessionID, record.Provider, record.SessionID, record.TurnID, record.ID, reply.ID)
	return nil
}

func (api *StreamingAPI) settleNativeForeground(sessionID string, event storeevents.Event) {
	if event.Type != "unified_completion" || event.Data == nil {
		return
	}
	if event.ExecutionKind != "" && event.ExecutionKind != "main_agent" && event.ExecutionKind != "main" && event.ExecutionKind != "chat" {
		return
	}
	completion, ok := event.Data.Data.(*agentevents.UnifiedCompletionEvent)
	if !ok || completion == nil {
		return
	}
	session, ok := mcpagent.LookupSession(sessionID)
	if !ok {
		return
	}
	handle := session.Snapshot()
	if handle == nil {
		return
	}
	delivered := completion.Status == "completed" && strings.TrimSpace(completion.FinalResult) != ""
	if err := nativereplies.SettleForeground(string(handle.Provider.Provider), handle.Provider.NativeSessionID, delivered); err != nil {
		log.Printf("[NATIVE_RECORD] gap session=%s reason=foreground_claim_settlement_failed", sessionID)
		return
	}
	for _, claim := range nativereplies.Claims(string(handle.Provider.Provider), handle.Provider.NativeSessionID) {
		log.Printf("[NATIVE_RECORD] foreground_settled session=%s provider=%s native=%s turn=%s committed=%t owner=%s completion=%s", sessionID, claim.Provider, claim.Session, claim.Root, claim.Committed, claim.Owner, event.ID)
	}
}

func (api *StreamingAPI) nativeCodingBackgroundWork(sessionID string) []SessionBackgroundWorkItem {
	items := []SessionBackgroundWorkItem{}
	if api == nil {
		return items
	}
	api.nativeObserversMu.Lock()
	var observers []*nativeTerminalObserver
	for _, o := range api.nativeObservers {
		if o.snapshot.SessionID == sessionID {
			observers = append(observers, o)
		}
	}
	api.nativeObserversMu.Unlock()
	for _, o := range observers {
		o.mu.Lock()
		if s := o.nativeRecords; s != nil {
			for id, r := range s.Helpers {
				if r.State == "started" || r.State == "scheduled" || r.State == "proposed" {
					items = append(items, SessionBackgroundWorkItem{ID: "native-helper:" + id, Kind: "native_helper", Label: "Coding agent helper", StartedAt: r.Timestamp, CanStop: false, Status: r.State})
				}
			}
			for id, r := range s.Turns {
				if r.State == "started" {
					items = append(items, SessionBackgroundWorkItem{ID: "native-turn:" + id, Kind: "native_turn", Label: "Coding agent follow-up", StartedAt: r.Timestamp, CanStop: false, Status: "Working"})
				}
			}
		}
		o.mu.Unlock()
	}
	return items
}
