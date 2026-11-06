package server

// Always on, step 3, the Mac's side (crew_agent_handback.go has the server's): when the owner's Mac is online again and
// the server answered for one of its agents (writer=server), the Mac takes each of the agent's conversations back —
// the owner's own chat and every teammate thread the server ran — and only then asks the server to hand the agent
// back. Per conversation: if the server only continued what the Mac has, the server's version is imported into the
// Mac's chat (the same native session id, its history seeded); if the Mac changed it too (it ran turns while offline),
// the Mac keeps its own and saves the server's aside, and the owner is told. Nothing is merged. Until the server flips
// the writer, the Mac refuses to run the agent and the server keeps answering, so no message is lost.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/sessiontransfer"
)

// crewHandbackRetry is how long the Mac waits before asking again while a server turn of the agent runs.
var crewHandbackRetry = 15 * time.Second

// crewHandbackRunning holds one hand back per agent at a time on this Mac.
var crewHandbackRunning sync.Map

// crewHandbackImportForTest stands in for the Mac's import (it needs a provider runtime) in tests.
var crewHandbackImportForTest func(roots crewroot.Roots, key string, conversation *crewHandoffConversation) error

// handBackCrewStandby takes one agent back from the server. Any failure leaves the server answering for it; the next
// reconnect, refused turn or standby send tries again.
func (api *StreamingAPI) handBackCrewStandby(project, tab string) {
	running := project + "\x00" + tab
	if _, busy := crewHandbackRunning.LoadOrStore(running, true); busy {
		return
	}
	defer crewHandbackRunning.Delete(running)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	claims, binding, connection, err := api.desktopCrewChatBinding(ctx, project)
	if err != nil || binding.Mode != "relay" {
		return
	}
	roots, err := api.crewSyncRoots(ctx, claims, project)
	if err != nil {
		return
	}
	call := func(action string, sessions []crewStandbySendItem) (*crewStandbyReply, error) {
		reply, err := desktopCrewSyncCall(ctx, claims.UserID, &connection, binding.PeerProjectID, crewSyncRequest{Action: action, PairID: binding.PairID, Standby: &crewStandbyRequest{Tab: tab, Sessions: sessions}})
		if err != nil {
			return nil, err
		}
		if reply.Standby == nil {
			return nil, fmt.Errorf("server_not_updated")
		}
		return reply.Standby, nil
	}
	// The outcome is in the [ALWAYS_ON] log lines.
	_, _, _ = api.runCrewHandback(ctx, project, tab, roots, call, func(b *sessiontransfer.Bundle) error {
		return desktopCrewNativePull(ctx, roots, connection, binding, b)
	}, binding.PairID)
}

// runCrewHandback is the hand back's loop, with the server calls and the native pull passed in.
func (api *StreamingAPI) runCrewHandback(ctx context.Context, project, tab string, roots crewroot.Roots, call func(string, []crewStandbySendItem) (*crewStandbyReply, error), pull func(*sessiontransfer.Bundle) error, pair string) (done bool, diverged int, err error) {
	started := time.Now()
	var bytes int64
	fail := func(reason string, e error) (bool, int, error) {
		crewHandbackLog(project, tab, "handback_failed", reason, bytes, started)
		if e != nil {
			log.Printf("[ALWAYS_ON] project=%s tab=%s handback_error=%q", project, tab, e)
		}
		return false, 0, e
	}
	wait := func() bool {
		select {
		case <-ctx.Done():
			return false
		case <-time.After(crewHandbackRetry):
			return true
		}
	}
	logged := false
	for {
		list, e := call("handback_pull", nil)
		if e != nil {
			return fail(crewHandbackReason(e), e)
		}
		if list.Writer != "server" {
			noteCrewStandbyWriter(project, tab, list.Writer)
			return false, 0, nil // already the Mac's
		}
		if !logged {
			crewHandbackLog(project, tab, "handback_started", "", 0, started)
			logged = true
		}
		if list.Busy {
			if !wait() {
				return fail("server_busy", ctx.Err())
			}
			continue
		}
		taken := []crewStandbySendItem{}
		retry := false
		diverged = 0
		for _, key := range list.Keys {
			one, e := call("handback_pull", []crewStandbySendItem{{Key: key}})
			if e != nil {
				return fail(crewHandbackReason(e), e)
			}
			if one.Busy {
				retry = true
				break
			}
			if one.Handback == nil || one.Handback.Key != key {
				return fail("bad_reply", nil)
			}
			event, n, e := api.applyCrewHandback(ctx, roots, tab, pair, *one.Handback, pull)
			if errors.Is(e, errCrewNativeDestinationBusy) {
				retry = true
				break
			}
			if e != nil {
				return fail("take_failed", e)
			}
			bytes += n
			if event == "diverged" {
				diverged++
			}
			taken = append(taken, crewStandbySendItem{Key: key, Fingerprint: one.Handback.Fingerprint, Diverged: event == "diverged"})
		}
		if retry {
			if !wait() {
				return fail("busy", ctx.Err())
			}
			continue
		}
		reply, e := call("handback_done", taken)
		if e != nil {
			return fail(crewHandbackReason(e), e)
		}
		if reply.Busy || reply.Event == "changed" {
			// A turn ran on the server after these were taken (a teammate's new ask): take the newer ones again.
			if !wait() {
				return fail("server_changed", ctx.Err())
			}
			continue
		}
		if reply.Writer == "server" {
			return fail("not_handed_back", nil)
		}
		noteCrewStandbyWriter(project, tab, "")
		api.resendCrewStandby(project, tab)
		crewHandbackLog(project, tab, "handback_done", fmt.Sprintf("conversations_%d", len(taken)), bytes, started)
		if diverged > 0 {
			crewHandbackLog(project, tab, "handback_diverged", fmt.Sprintf("conversations_%d", diverged), bytes, started)
		}
		return true, diverged, nil
	}
}

func crewHandbackReason(err error) string {
	var failure *crewSyncFailure
	if errors.As(err, &failure) && failure.Code != "" {
		return failure.Code
	}
	return "server_unavailable"
}

// resendCrewStandby makes the agent's next standby send offer every conversation: the ones just taken back have new
// sessions on this Mac, and the server's copy must catch up with them.
func (api *StreamingAPI) resendCrewStandby(project, tab string) {
	crewStandbyMu.Lock()
	if agent := crewStandbyAgents[project+"\x00"+tab]; agent != nil {
		agent.primed = false
	}
	crewStandbyMu.Unlock()
	api.scheduleCrewStandby(project, tab, "", 5*time.Second)
}

// applyCrewHandback takes one conversation back: imported, unchanged (the Mac has it already), mac_newer (the server
// did not change it; the Mac did), diverged (both changed: the Mac keeps its own, the server's is saved aside) or
// no_native (nothing to take). The bytes are the server copy's size.
func (api *StreamingAPI) applyCrewHandback(ctx context.Context, roots crewroot.Roots, tab, pair string, item crewStandbySendItem, pull func(*sessiontransfer.Bundle) error) (string, int64, error) {
	if item.Conversation == nil || item.Conversation.Native == nil {
		return "no_native", 0, nil
	}
	if crewStandbyKeyTab(roots.ID, item.Key) != tab {
		return "", 0, fmt.Errorf("a conversation does not belong to this agent")
	}
	conversation := *item.Conversation
	server := *conversation.Native
	conversation.Native = &server
	if err := server.Validate(); err != nil {
		return "", 0, err
	}
	if err := validateCrewHandoffConversation(&conversation); err != nil {
		return "", 0, err
	}
	var bytes int64
	for _, file := range server.Files {
		bytes += file.Size
	}
	claims := &UserClaims{UserID: roots.Owner, Username: roots.Owner}
	ctx = context.WithValue(ctx, UserContextKey, claims)
	var record *ProductConversationRecord
	var mine *sessiontransfer.Bundle
	if crewHandbackMineForTest != nil {
		record, mine = crewHandbackMineForTest(roots, item.Key)
	} else {
		for _, r := range crewStandbyRecords(roots) {
			if crewStandbyWireKey(roots.ID, r.ConversationKey) == item.Key {
				record = &r
			}
		}
	}
	// The Mac's own version, captured into the session's native stage and copied aside: the pull then sends only what
	// differs, and both versions can be compared.
	scratch, err := os.MkdirTemp(roots.Private, "handback-mine-")
	if err != nil {
		return "", 0, err
	}
	defer os.RemoveAll(scratch)
	if record != nil {
		if api.crewAgentTurnRunning(record.SessionID) {
			return "", 0, errCrewNativeDestinationBusy
		}
		if mine == nil && crewHandbackMineForTest == nil {
			if c, e := api.captureCrewHandoffConversation(ctx, claims, roots, *record, true); e == nil && c != nil && c.Native != nil {
				mine = c.Native
			}
		}
		if mine != nil {
			if mine, err = copyCrewBundle(mine, filepath.Join(scratch, "native")); err != nil {
				return "", 0, err
			}
		}
	}
	// The server answered on another program than this Mac's (it lacked the Mac's, crewImportNativeElseHistory): its
	// native session can't resume here, so this Mac takes its saved history instead. When the Mac's own history is the
	// start of the server's, the chat continues with all of it and the next turn starts a fresh native conversation on
	// this Mac's program from the usual continuity notice, as "Fresh chat" does; otherwise both changed and the server's
	// version is kept aside.
	if macProgram := crewHandbackMacProgram(record, mine); macProgram != "" && macProgram != server.Provider {
		if record == nil || !crewHandbackHistoryExtends(claims.UserID, *record, conversation.History) {
			return "diverged", bytes, keepCrewHandbackAside(roots, tab, item.Key, &conversation)
		}
		if api.crewAgentTurnRunning(record.SessionID) {
			return "", 0, errCrewNativeDestinationBusy
		}
		history := conversation
		history.Native = nil
		sum := sha256.Sum256([]byte(item.Key))
		transfer := fmt.Sprintf("always-on/%s/%s/%s", tab, hex.EncodeToString(sum[:8]), item.Fingerprint)
		if crewHandbackImportForTest != nil {
			err = crewHandbackImportForTest(roots, item.Key, &history)
		} else {
			main, e := api.crewHandoffMain(ctx, claims, roots.ID)
			if e != nil {
				return "", 0, e
			}
			record.handoffAgentToolsMode = main.handoffAgentToolsMode
			err = api.importCrewHandoffConversation(ctx, claims, roots, pair, transfer, &history, *record, crewImportNativeElseHistory)
		}
		if err != nil {
			return "", 0, err
		}
		log.Printf("[ALWAYS_ON] project=%s tab=%s event=handback_imported mode=history mac_program=%s server_program=%s messages=%d", roots.ID, tab, macProgram, server.Provider, len(history.History))
		return "imported", bytes, nil
	}
	if err = pull(&server); err != nil {
		return "", 0, err
	}
	event := crewHandbackDecide(mine, &server)
	switch event {
	case "unchanged", "mac_newer":
		return event, bytes, nil
	case "diverged":
		return event, bytes, keepCrewHandbackAside(roots, tab, item.Key, &conversation)
	}
	if record == nil && !isSharedThreadKey(item.Key) {
		// The Mac's tab was removed while the server answered for it: the server's version stays on the server.
		return "no_tab", bytes, nil
	}
	if crewHandbackImportForTest != nil {
		err = crewHandbackImportForTest(roots, item.Key, &conversation)
	} else {
		if record == nil {
			created, e := api.crewHandbackThread(ctx, claims, item.Key)
			if e != nil {
				return "", 0, e
			}
			record = &created
		}
		main, e := api.crewHandoffMain(ctx, claims, roots.ID)
		if e != nil {
			return "", 0, e
		}
		record.handoffAgentToolsMode = main.handoffAgentToolsMode
		sum := sha256.Sum256([]byte(item.Key))
		transfer := fmt.Sprintf("always-on/%s/%s/%s", tab, hex.EncodeToString(sum[:8]), item.Fingerprint)
		err = api.importCrewHandoffConversation(ctx, claims, roots, pair, transfer, &conversation, *record, crewImportNative)
	}
	if errors.Is(err, sessiontransfer.ErrDiverged) {
		return "diverged", bytes, keepCrewHandbackAside(roots, tab, item.Key, &conversation)
	}
	if err != nil {
		return "", 0, err
	}
	log.Printf("[ALWAYS_ON] project=%s tab=%s event=handback_imported provider=%s bytes=%d", roots.ID, tab, server.Provider, bytes)
	return "imported", bytes, nil
}

// crewHandbackMacProgram is the AI program this Mac runs the conversation on.
func crewHandbackMacProgram(record *ProductConversationRecord, mine *sessiontransfer.Bundle) string {
	if mine != nil {
		return mine.Provider
	}
	if record != nil {
		return strings.TrimSpace(record.Provider)
	}
	return ""
}

// crewHandbackHistoryExtends: this Mac's saved history of the conversation is the start of the server's, so the server
// only added turns (the Mac did not answer in it meanwhile).
func crewHandbackHistoryExtends(owner string, record ProductConversationRecord, server []llmtypes.MessageContent) bool {
	raw, err := ReadChatHistoryConversation(owner, record.SessionID, record.WorkspacePath)
	if err != nil {
		return errors.Is(err, errChatHistoryConversationNotFound)
	}
	var doc struct {
		History []llmtypes.MessageContent `json:"conversation_history"`
	}
	if json.Unmarshal(raw, &doc) != nil || len(doc.History) > len(server) {
		return false
	}
	for i := range doc.History {
		a, _ := json.Marshal(doc.History[i])
		b, _ := json.Marshal(server[i])
		if !bytes.Equal(a, b) {
			return false
		}
	}
	return true
}

// crewHandbackMineForTest stands in for finding and capturing the Mac's own version (it needs a provider runtime) in
// tests.
var crewHandbackMineForTest func(roots crewroot.Roots, key string) (*ProductConversationRecord, *sessiontransfer.Bundle)

// crewHandbackDecide compares the Mac's version of a conversation with the server's (both with their files on disk).
func crewHandbackDecide(mine, server *sessiontransfer.Bundle) string {
	switch {
	case mine == nil:
		return "import"
	case mine.Provider != server.Provider || mine.SessionID != server.SessionID:
		return "diverged"
	case bundleFingerprint(mine) == bundleFingerprint(server):
		return "unchanged"
	case crewStandbyAncestor(mine, server) == nil:
		return "import"
	case crewStandbyAncestor(server, mine) == nil:
		return "mac_newer"
	}
	return "diverged"
}

func isSharedThreadKey(key string) bool {
	_, chatID, ok := strings.Cut(key, codeChatSideMarker)
	return ok && strings.HasPrefix(chatID, sharedThreadChatPrefix)
}

// crewHandbackThread makes this Mac's record for a teammate's thread that started on the server while the Mac was off.
func (api *StreamingAPI) crewHandbackThread(ctx context.Context, claims *UserClaims, key string) (ProductConversationRecord, error) {
	profile, err := api.agentProfiles.Resolve(crewProfileID, 0, claims.UserID)
	if err != nil {
		return ProductConversationRecord{}, err
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://localhost/api/agent-profiles/work/conversation", nil)
	if err != nil {
		return ProductConversationRecord{}, err
	}
	r = mux.SetURLVars(r, map[string]string{"id": crewProfileID})
	return api.resolveAgentProfileConversation(r, profile, key, "")
}

func copyCrewBundle(b *sessiontransfer.Bundle, dest string) (*sessiontransfer.Bundle, error) {
	out := *b
	for name, meta := range b.Files {
		source, err := sessiontransfer.Path(b.Root, name, false)
		if err != nil {
			return nil, err
		}
		target := filepath.Join(dest, filepath.FromSlash(name))
		if err = os.MkdirAll(filepath.Dir(target), 0o700); err == nil {
			err = copyCrewStandbyFile(source, target, meta.Size)
		}
		if err != nil {
			return nil, err
		}
	}
	out.Root = dest
	if err := out.ValidateFiles(); err != nil {
		return nil, err
	}
	return &out, nil
}

// crewHandbackKeptDir is where this Mac keeps the server's version of a conversation it also changed itself.
func crewHandbackKeptDir(roots crewroot.Roots, tab string) string {
	return filepath.Join(roots.Private, "always-on-kept", tab)
}

// keepCrewHandbackAside saves the server's version (native session and history) next to the Mac's private Crew state,
// never over the Mac's own (owner, 5 Nov: keep both, no merge).
func keepCrewHandbackAside(roots crewroot.Roots, tab, key string, conversation *crewHandoffConversation) error {
	sum := sha256.Sum256([]byte(key))
	dir := filepath.Join(crewHandbackKeptDir(roots, tab), hex.EncodeToString(sum[:8])+"-"+time.Now().UTC().Format("20060102T150405.000000000Z"))
	kept, err := copyCrewBundle(conversation.Native, filepath.Join(dir, "native"))
	if err != nil {
		return err
	}
	if err = fsutil.WriteJSONAtomic(filepath.Join(dir, "native.json"), kept, 0o600); err != nil {
		return err
	}
	history := *conversation
	history.Native = nil
	raw, err := json.Marshal(history)
	if err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(dir, "conversation.json"), raw, 0o600); err != nil {
		return err
	}
	log.Printf("[ALWAYS_ON] project=%s tab=%s event=handback_kept_server_version copy=%s", roots.ID, tab, filepath.Base(dir))
	return nil
}
