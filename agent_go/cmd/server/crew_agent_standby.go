package server

// Always on, step 1 (owner, 5 Nov: every In team agent is always on): the owner's Mac keeps a standby copy of each
// In team agent's conversation on its Crew's online copy — the native session of the owner's tab and of every teammate
// thread on that agent, with its AgentWorks history. The Mac still runs the agent; nothing here routes a turn to the
// server (step 2 does). The copy arrives through the same native sync as Publish (handleCrewNativeSync) and is admitted
// by the same owner and pair checks (crewTransferBinding).
//
// The standby record is kept apart from the Publish placements on purpose: every placement reader treats an entry as
// "published" (routing, runtime folder, callers, reach off), and a standby agent is not published.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/multi-llm-provider-go/pkg/sessiontransfer"
)

// crewStandbyRequest: standby_check names the agent (and, per session, the fingerprint the Mac has now);
// standby_store carries exactly one session whose native bundle was just synced to this server's native stage.
type crewStandbyRequest struct {
	Tab      string                `json:"tab"`
	Sessions []crewStandbySendItem `json:"sessions,omitempty"`
	// MacOnly (standby_check): what this Crew uses that only the owner's Mac has, names only (crewTeamMacOnly), for the
	// note a server turn gets (crewStandbyServerNote). Nil keeps the last list.
	MacOnly []crewTeamMacOnlyItem `json:"mac_only"`
}

type crewStandbySendItem struct {
	// Key is the Mac conversation key: the owner's tab, or a teammate's thread on it (sharedThreadKey).
	Key          string                   `json:"key"`
	Fingerprint  string                   `json:"fingerprint"`
	Conversation *crewHandoffConversation `json:"conversation,omitempty"`
	// Diverged (handback_done): the Mac had changed this conversation too, so it kept its own and saved the server's
	// aside (crew_agent_handback.go).
	Diverged bool `json:"diverged,omitempty"`
}

type crewStandbyReply struct {
	// Eligible: the agent is In team in a Team that is on, and not published. Not eligible drops any copy.
	Eligible bool     `json:"eligible"`
	Reason   string   `json:"reason,omitempty"`
	Need     []string `json:"need,omitempty"`  // standby_check: keys whose copy differs from the Mac's fingerprint
	Event    string   `json:"event,omitempty"` // standby_store: stored, unchanged, diverged or server_writer
	Revision uint64   `json:"revision,omitempty"`
	// Writer is "server" while the server answers for the agent from its copy (step 2, crew_agent_always_on.go): the
	// Mac then neither runs the agent nor sends copies of it until it is handed back.
	Writer string `json:"writer,omitempty"`
	// Hand back (Always on, step 3, crew_agent_handback.go). Busy: a server turn of the agent is running, ask again
	// later. Keys (handback_pull without a session): the agent's conversations on the server, as the Mac names them.
	// Handback (handback_pull with one key): that conversation, its native bundle staged for the pull.
	Busy     bool                 `json:"busy,omitempty"`
	Keys     []string             `json:"keys,omitempty"`
	Handback *crewStandbySendItem `json:"handback,omitempty"`
}

// crewAgentStandby is one agent's standby copy on the server: "standby" means the server holds a current copy and the
// Mac runs the agent.
type crewAgentStandby struct {
	Tab      string                             `json:"tab"`
	Location string                             `json:"location"`
	PairID   string                             `json:"pair_id"`
	Revision uint64                             `json:"revision"`
	CopiedAt string                             `json:"copied_at"`
	Sessions map[string]crewAgentStandbySession `json:"sessions"`
	// Writer is "server" once the server ran a turn of the agent from this copy (Always on, step 2), with the copy's
	// revision then and the time; empty while the Mac runs it. Step 3 hands it back to the Mac.
	Writer         string `json:"writer,omitempty"`
	WriterRevision uint64 `json:"writer_revision,omitempty"`
	WriterSince    string `json:"writer_since,omitempty"`
	// KeptAt: the last hand back found the agent had answered on both the Mac and the server; both versions were kept
	// and the owner was told (crew_agent_handback.go).
	KeptAt string `json:"kept_at,omitempty"`
	// MacProgram / ServerProgram: the AI program the agent runs on the Mac and the one the server continued it on. They
	// differ when this server lacks the Mac's program and continued from saved history instead (owner, 6 Nov).
	MacProgram    string `json:"mac_program,omitempty"`
	ServerProgram string `json:"server_program,omitempty"`
}

type crewAgentStandbySession struct {
	Dir             string   `json:"dir"`
	Fingerprint     string   `json:"fingerprint"`
	ConversationID  string   `json:"conversation_id"`
	Provider        string   `json:"provider"`
	NativeSessionID string   `json:"native_session_id"`
	Bytes           int64    `json:"bytes"`
	Revision        uint64   `json:"revision"`
	StoredAt        string   `json:"stored_at"`
	Diverged        []string `json:"diverged,omitempty"` // earlier copies kept because the new one did not extend them
	// Imported is the revision of this copy the server's own chat was last made from (crew_agent_always_on.go).
	Imported uint64 `json:"imported,omitempty"`
}

var crewStandbyLocks sync.Map

func crewStandbyLock(project string) *sync.Mutex {
	value, _ := crewStandbyLocks.LoadOrStore(project, &sync.Mutex{})
	return value.(*sync.Mutex)
}

// crewStandbyDir is app-private state next to the Crew's sync state (removed and backed up with it).
func crewStandbyDir(project string) (string, error) {
	p, err := crewSyncManifestPath(project)
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(p), project+"-always-on"), nil
}

func readCrewStandby(project string) (map[string]crewAgentStandby, error) {
	dir, err := crewStandbyDir(project)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(filepath.Join(dir, "standby.json"))
	if os.IsNotExist(err) {
		return map[string]crewAgentStandby{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := map[string]crewAgentStandby{}
	if err = json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// crewStandbyHistoryEvents is the saved dialogue of the stored copy of one of this Crew's Mac conversations, as chat
// events, for reading it while the Mac is off.
func crewStandbyHistoryEvents(project, conversationID string) []json.RawMessage {
	rows, err := readCrewStandby(project)
	if err != nil || conversationID == "" {
		return nil
	}
	dir, err := crewStandbyDir(project)
	if err != nil {
		return nil
	}
	for tab, row := range rows {
		for _, session := range row.Sessions {
			if session.ConversationID != conversationID {
				continue
			}
			raw, err := os.ReadFile(filepath.Join(dir, tab, session.Dir, "conversation.json"))
			if err != nil {
				return nil
			}
			var copied crewHandoffConversation
			if json.Unmarshal(raw, &copied) != nil {
				return nil
			}
			data, _ := json.Marshal(map[string]any{"session_id": conversationID, "conversation_history": copied.History, "updated_at": session.StoredAt})
			var log builderConversationLog
			if json.Unmarshal(data, &log) != nil {
				return nil
			}
			return builderConversationToRawEventsLimit(log, 0)
		}
	}
	return nil
}

// crewStandbyMacOnly is the last list of what only the owner's Mac has, as its Mac sent it.
func crewStandbyMacOnly(project string) []crewTeamMacOnlyItem {
	dir, err := crewStandbyDir(project)
	if err != nil {
		return nil
	}
	var items []crewTeamMacOnlyItem
	if raw, err := os.ReadFile(filepath.Join(dir, "mac-only.json")); err == nil {
		_ = json.Unmarshal(raw, &items)
	}
	return items
}

func writeCrewStandby(project string, rows map[string]crewAgentStandby) error {
	dir, err := crewStandbyDir(project)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return fsutil.WriteJSONAtomic(filepath.Join(dir, "standby.json"), rows, 0o600)
}

func crewStandbyLog(project, tab, event string, bytes int64, started time.Time, extra string) {
	log.Printf("[ALWAYS_ON] project=%s tab=%s event=%s bytes=%d ms=%d%s", project, tab, event, bytes, time.Since(started).Milliseconds(), extra)
}

// crewStandbyFingerprint names one session's copy: its native files and its history. Both sides compute it from the
// content itself, so the server never trusts the Mac's value.
func crewStandbyFingerprint(c *crewHandoffConversation) string {
	if c == nil || c.Native == nil {
		return ""
	}
	history, _ := json.Marshal(c.History)
	sum := sha256.New()
	fmt.Fprintf(sum, "%s\x00%s\x00%s\x00", c.ConversationID, bundleFingerprint(c.Native), c.ModelID)
	sum.Write(history)
	return hex.EncodeToString(sum.Sum(nil))
}

// crewStandbyEligible is the server's answer to "keep a copy of this agent": the Crew is the owner's Mac Crew's online
// copy, its Team is on, the agent is listed for the Team, it is In team (the owner's own choice, else All agents in
// team), and it is not published (a published agent already runs here).
func crewStandbyEligible(project, owner, tab string) (bool, string) {
	if !crewRelayTwin(project) {
		return false, "not_mac_crew"
	}
	t, err := readCrewTeam(project)
	if err != nil || !t.On {
		return false, "team_off"
	}
	if owner == "" || t.OwnerID != owner || ownerOfCrewProjectID(project) != owner {
		return false, "not_owner"
	}
	if !slices.ContainsFunc(readMacSharedAgents(project), func(a macSharedAgent) bool { return a.Tab == tab && crewTeamInTeam(a) }) {
		return false, "not_listed"
	}
	if !t.agentInTeam(owner, tab) {
		return false, "only_me"
	}
	if placements, err := readCrewAgentPlacements(project); err != nil {
		return false, "placement_unreadable"
	} else if _, published := placements[tab]; published {
		return false, "published"
	}
	return true, ""
}

// pruneCrewStandby drops every standby copy that is no longer allowed: the agent turned Only me or left the Team's
// list, the Team turned off, the agent was published, or the Crew's pair changed. Runs after each Team record and
// shared-agent list write, and on each standby request.
func pruneCrewStandby(project string) {
	if IsLocalMode() {
		return
	}
	dir, err := crewStandbyDir(project)
	if err != nil {
		return
	}
	if _, err = os.Stat(filepath.Join(dir, "standby.json")); err != nil {
		return
	}
	lock := crewStandbyLock(project)
	lock.Lock()
	defer lock.Unlock()
	rows, err := readCrewStandby(project)
	if err != nil {
		return
	}
	binding, _ := crewroot.ReadSync(project)
	owner := ownerOfCrewProjectID(project)
	changed := false
	for tab, row := range rows {
		reason := ""
		if binding == nil || binding.PairID != row.PairID {
			reason = "pair_changed"
		} else if ok, why := crewStandbyEligible(project, owner, tab); !ok && row.Writer != "server" {
			// A copy the server answers from holds turns the Mac does not have yet: it stays until handed back.
			reason = why
		}
		if reason == "" {
			continue
		}
		started := time.Now()
		_ = os.RemoveAll(filepath.Join(dir, tab))
		delete(rows, tab)
		changed = true
		crewStandbyLog(project, tab, "dropped", 0, started, " reason="+reason)
	}
	if changed {
		if err = writeCrewStandby(project, rows); err != nil {
			log.Printf("[ALWAYS_ON] project=%s event=drop_record_failed", project)
		}
	}
}

// dropCrewStandbyTab removes one agent's copy at once (a standby request for an agent that is no longer eligible).
// The caller holds the project's standby lock.
func dropCrewStandbyTab(project, tab, reason string) {
	rows, err := readCrewStandby(project)
	if err != nil {
		return
	}
	if row, ok := rows[tab]; !ok || row.Writer == "server" {
		return // a copy the server answers from stays until it is handed back to the Mac
	}
	started := time.Now()
	if dir, err := crewStandbyDir(project); err == nil {
		_ = os.RemoveAll(filepath.Join(dir, tab))
	}
	delete(rows, tab)
	if writeCrewStandby(project, rows) == nil {
		crewStandbyLog(project, tab, "dropped", 0, started, " reason="+reason)
	}
}

// crewStandbyKeyTab: the Mac conversation key must be this agent's own tab or a teammate's thread on it, in the
// paired Mac Crew.
func crewStandbyKeyTab(peer, key string) string {
	if key != peer && !strings.HasPrefix(key, peer+codeChatSideMarker) {
		return ""
	}
	return crewAgentPublishTab(key)
}

func (api *StreamingAPI) receiveCrewStandby(ctx context.Context, claims *UserClaims, id string, in crewSyncRequest) (crewSyncReply, error) {
	roots, binding, err := api.crewTransferBinding(ctx, claims, id, in.PairID)
	if err != nil {
		return crewSyncReply{}, err
	}
	out := crewSyncReply{ProjectID: id, Binding: binding}
	if binding.Mode != "relay" || binding.Editing != "local" || binding.PendingAction != "" || binding.ArchiveOnly {
		return out, &crewSyncFailure{Status: 409, Code: "wrong_pair", Message: "keep a standby copy from the Mac that holds this Crew"}
	}
	if in.Standby == nil || !crewMacTabID(in.Standby.Tab) {
		return out, fmt.Errorf("choose one of this Crew's agents")
	}
	tab := in.Standby.Tab
	lock := crewStandbyLock(id)
	lock.Lock()
	defer lock.Unlock()
	reply := &crewStandbyReply{}
	out.Standby = reply
	rows, err := readCrewStandby(id)
	if err != nil {
		return out, err
	}
	row := rows[tab]
	if row.PairID != "" && row.PairID != binding.PairID {
		row = crewAgentStandby{}
	}
	reply.Writer = row.Writer
	if ok, reason := crewStandbyEligible(id, claims.UserID, tab); !ok {
		reply.Reason = reason
		dropCrewStandbyTab(id, tab, reason)
		return out, nil
	}
	reply.Eligible = true
	reply.Revision = row.Revision
	if in.Action == "standby_check" {
		if in.Standby.MacOnly != nil {
			if dir, err := crewStandbyDir(id); err == nil && os.MkdirAll(dir, 0o700) == nil {
				_ = fsutil.WriteJSONAtomic(filepath.Join(dir, "mac-only.json"), in.Standby.MacOnly, 0o600)
			}
		}
		for _, item := range in.Standby.Sessions {
			if crewStandbyKeyTab(binding.PeerProjectID, item.Key) != tab {
				return out, fmt.Errorf("a conversation does not belong to this agent")
			}
			if row.Sessions[item.Key].Fingerprint != item.Fingerprint {
				reply.Need = append(reply.Need, item.Key)
			}
		}
		return out, nil
	}
	if len(in.Standby.Sessions) != 1 {
		return out, fmt.Errorf("send one conversation at a time")
	}
	item := in.Standby.Sessions[0]
	started := time.Now()
	if row.Writer == "server" {
		// The server answers for this agent: its chats here are newer than the Mac's copy, which must not replace them.
		reply.Event = "server_writer"
		crewStandbyLog(id, tab, "server_writer", 0, started, "")
		return out, nil
	}
	if crewStandbyKeyTab(binding.PeerProjectID, item.Key) != tab {
		return out, fmt.Errorf("a conversation does not belong to this agent")
	}
	conversation := item.Conversation
	if conversation == nil || conversation.Native == nil {
		return out, fmt.Errorf("a standby copy needs the agent's native session")
	}
	if err = validateCrewHandoffConversation(conversation); err != nil {
		return out, err
	}
	native := conversation.Native
	if err = native.Validate(); err != nil {
		return out, err
	}
	fingerprint := crewStandbyFingerprint(conversation)
	sum := sha256.Sum256([]byte(item.Key))
	name := hex.EncodeToString(sum[:8])
	previous, had := row.Sessions[item.Key]
	if had && previous.Fingerprint == fingerprint {
		reply.Event = "unchanged"
		crewStandbyLog(id, tab, "unchanged", 0, started, " copy="+name)
		return out, nil
	}
	dir, err := crewStandbyDir(id)
	if err != nil {
		return out, err
	}
	final := filepath.Join(dir, tab, name)
	// The bytes are the ones the native sync just staged for this session; the stage lock keeps a concurrent push
	// from changing them while they are checked and copied.
	nativeLock := crewNativeLock(roots, native.Provider+"-"+native.SessionID)
	nativeLock.Lock()
	defer nativeLock.Unlock()
	if err = bindCrewNativeStage(roots, native); err != nil {
		return out, fmt.Errorf("the standby copy did not arrive complete; send it again")
	}
	event := "stored"
	if had {
		old, e := readCrewStandbyBundle(final)
		if e == nil {
			e = crewStandbyAncestor(old, native)
		}
		if e != nil {
			event = "diverged"
		}
	}
	if err = os.MkdirAll(filepath.Join(dir, tab), 0o700); err != nil {
		return out, err
	}
	tmp, err := os.MkdirTemp(filepath.Join(dir, tab), "."+name+"-")
	if err != nil {
		return out, err
	}
	defer os.RemoveAll(tmp)
	var bytes int64
	for file, meta := range native.Files {
		source, e := sessiontransfer.Path(native.Root, file, false)
		if e != nil {
			return out, e
		}
		dest := filepath.Join(tmp, "native", filepath.FromSlash(file))
		if e = os.MkdirAll(filepath.Dir(dest), 0o700); e == nil {
			e = copyCrewStandbyFile(source, dest, meta.Size)
		}
		if e != nil {
			return out, e
		}
		bytes += meta.Size
	}
	stored := *native
	stored.Root = filepath.Join(tmp, "native")
	if err = stored.ValidateFiles(); err != nil {
		return out, fmt.Errorf("the standby copy changed while it was saved; send it again")
	}
	if err = fsutil.WriteJSONAtomic(filepath.Join(tmp, "native.json"), &stored, 0o600); err != nil {
		return out, err
	}
	history := *conversation
	history.Native = nil
	raw, err := json.Marshal(history)
	if err != nil {
		return out, err
	}
	bytes += int64(len(raw))
	if err = os.WriteFile(filepath.Join(tmp, "conversation.json"), raw, 0o600); err != nil {
		return out, err
	}
	now := time.Now().UTC()
	session := crewAgentStandbySession{Dir: name, Fingerprint: fingerprint, ConversationID: conversation.ConversationID, Provider: native.Provider, NativeSessionID: native.SessionID, Bytes: bytes, StoredAt: now.Format(time.RFC3339)}
	if had {
		session.Diverged = previous.Diverged
	}
	if _, e := os.Lstat(final); e == nil {
		// Diverged: the copy already here is kept beside the new one, never overwritten (owner, 5 Nov: keep both).
		aside := final + ".old"
		if event == "diverged" {
			kept := name + ".diverged-" + now.Format("20060102T150405.000000000Z")
			aside = filepath.Join(dir, tab, kept)
			session.Diverged = append(session.Diverged, kept)
		} else {
			_ = os.RemoveAll(aside) // a leftover from an interrupted earlier save
		}
		if err = os.Rename(final, aside); err != nil {
			return out, err
		}
		if event != "diverged" {
			defer os.RemoveAll(aside)
		}
	}
	if err = os.Rename(tmp, final); err != nil {
		return out, err
	}
	if row.Sessions == nil {
		row.Sessions = map[string]crewAgentStandbySession{}
	}
	row.Tab, row.Location, row.PairID = tab, "standby", binding.PairID
	row.Revision++
	row.CopiedAt = session.StoredAt
	session.Revision = row.Revision
	row.Sessions[item.Key] = session
	rows[tab] = row
	if err = writeCrewStandby(id, rows); err != nil {
		return out, err
	}
	reply.Event, reply.Revision = event, row.Revision
	crewStandbyLog(id, tab, "standby_stored", bytes, started, fmt.Sprintf(" copy=%s revision=%d", name, row.Revision))
	if event == "diverged" {
		crewStandbyLog(id, tab, "diverged", bytes, started, fmt.Sprintf(" copy=%s kept=%d", name, len(session.Diverged)))
	}
	return out, nil
}

func readCrewStandbyBundle(dir string) (*sessiontransfer.Bundle, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "native.json"))
	if err != nil {
		return nil, err
	}
	var b sessiontransfer.Bundle
	if err = json.Unmarshal(raw, &b); err != nil {
		return nil, err
	}
	b.Root = filepath.Join(dir, "native")
	return &b, nil
}

// crewStandbyAncestor: the new copy continues the stored one. Journals must extend it byte for byte
// (sessiontransfer.Ancestor). A database snapshot (Cursor) cannot be compared by prefix; it continues the stored one
// when it has the same snapshot or comes from the same last handoff (its parent).
func crewStandbyAncestor(old, next *sessiontransfer.Bundle) error {
	if strings.HasSuffix(next.Format, "sqlite") {
		if old.Provider != next.Provider || old.Format != next.Format || old.SessionID != next.SessionID {
			return sessiontransfer.ErrDiverged
		}
		if next.Snapshot == old.Snapshot || next.Parent == old.Parent || next.Parent == old.Snapshot {
			return nil
		}
		return sessiontransfer.ErrDiverged
	}
	return sessiontransfer.Ancestor(old, next)
}

func copyCrewStandbyFile(source, dest string, size int64) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = io.CopyN(out, in, size)
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	return err
}
