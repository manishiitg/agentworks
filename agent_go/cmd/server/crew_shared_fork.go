package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/multi-llm-provider-go/pkg/sessiontransfer"
)

// crewMacSharedAsk is another person's message to a shared agent on this Mac: who sent it and the owner's own chat
// with that agent, which their turn starts from (owner, 4 Nov, decisions log: "use it for everyone").
type crewMacSharedAsk struct {
	Sender string
	Owner  ProductConversationRecord
}

// forkOwnerSessionForSender gives a sender's thread a copy of the owner's own native session for that agent (the CLI's
// saved session file), so the agent answers knowing what the owner discussed. The owner's session is only read. When the
// owner's session is unchanged since this thread's last copy, the thread simply continues its own (nil is returned);
// otherwise the fresh copy replaces it and recap is set to the sender's last exchanges, which the new copy lacks. The
// copy is plain (owner, 4 Nov: they get what the agent has). Any failure leaves the thread on its own session.
func (api *StreamingAPI) forkOwnerSessionForSender(ctx context.Context, userID, sessionID string, ask *crewMacSharedAsk) (runtime *ChatHistoryAgentRuntime, recap string) {
	if ask == nil || ask.Owner.SessionID == "" || ask.Owner.SessionID == sessionID {
		return nil, ""
	}
	owner := ask.Owner
	skip := func(reason string) (*ChatHistoryAgentRuntime, string) {
		log.Printf("[SHARED_FORK] thread=%s owner_session=%s fork=no reason=%q", sessionID, owner.SessionID, reason)
		return nil, ""
	}
	roots, found, err := crewStorageRoots(owner.WorkspacePath)
	if err != nil || !found || roots.Owner != userID {
		return skip("crew folder unavailable")
	}
	ownerRuntime, ok, err := ReadChatHistoryRuntimeForSession(userID, owner.SessionID, owner.WorkspacePath)
	if err != nil || !ok || ownerRuntime == nil || ownerRuntime.AgentSessionHandle == nil {
		return skip("owner has no saved native session yet")
	}
	handle := ownerRuntime.AgentSessionHandle.Provider
	if handle.NativeSessionID == "" || handle.Provider == "" {
		return skip("owner has no saved native session yet")
	}
	owner.Provider = handle.Provider
	claims := &UserClaims{UserID: userID}
	store, runtimeDir, err := api.crewProgramStore(ctx, claims, roots, owner, handle.Transport, false)
	if err != nil {
		return skip("owner runtime unavailable: " + err.Error())
	}
	out, err := os.MkdirTemp(roots.Private, "native-fork-")
	if err != nil {
		return skip("no private staging")
	}
	defer os.RemoveAll(out)
	bundle, err := exportCrewNativeProgram(ctx, userID, runtimeDir, store, handle.WorkingDir, handle.NativeSessionID, handle.Provider, handle.Transport, out, false)
	if err != nil || bundle == nil {
		return skip(fmt.Sprintf("%s can't share its conversation yet: %v", handle.Provider, err))
	}
	fingerprint := bundleFingerprint(bundle)
	statePath := filepath.Join(roots.Private, "native-fork", sessionID+".json")
	var state struct {
		Owner string `json:"owner_fingerprint"`
	}
	if raw, e := os.ReadFile(statePath); e == nil && json.Unmarshal(raw, &state) == nil && state.Owner == fingerprint {
		return nil, ""
	}
	var size int64
	for _, file := range bundle.Files {
		size += file.Size
	}
	thread := owner
	thread.SessionID = sessionID
	closeWorkflowPolicyCLI(sessionID, handle.Provider, "a shared agent's thread starts from its owner's latest conversation")
	forked, err := api.importCrewNativeInto(ctx, claims, roots, thread, sessionID, bundle)
	if err != nil || forked == nil {
		return skip("copy could not be imported: " + fmt.Sprint(err))
	}
	if err = os.MkdirAll(filepath.Dir(statePath), 0700); err == nil {
		state.Owner = fingerprint
		err = fsutil.WriteJSONAtomic(statePath, state, 0600)
	}
	if err != nil {
		log.Printf("[SHARED_FORK] thread=%s could not record its copy: %v", sessionID, err)
	}
	log.Printf("[SHARED_FORK] thread=%s owner_session=%s fork=yes provider=%s native=%s files=%d bytes=%d", sessionID, owner.SessionID, handle.Provider, bundle.SessionID, len(bundle.Files), size)
	return forked, senderRecap(ctx, userID, sessionID, owner.WorkspacePath, ask.Sender)
}

func bundleFingerprint(b *sessiontransfer.Bundle) string {
	names := make([]string, 0, len(b.Files))
	for name := range b.Files {
		names = append(names, name)
	}
	sort.Strings(names)
	sum := sha256.New()
	for _, name := range names {
		fmt.Fprintf(sum, "%s\x00%s\x00", name, b.Files[name].SHA256)
	}
	return hex.EncodeToString(sum.Sum(nil))
}

// senderRecap is the sender's own last exchanges in their thread, for a turn that starts from a fresh copy of the
// owner's conversation (which does not hold them). Bounded so it stays a short note.
func senderRecap(ctx context.Context, userID, sessionID, workspace, sender string) string {
	items, _, err := codeChatTranscript(ctx, userID, sessionID, workspace, 0, 0)
	if err != nil {
		return ""
	}
	lines := []string{}
	for _, item := range senderOwnTurns(items, sender) {
		if item.Role != "you" && item.Role != "assistant" {
			continue
		}
		text := strings.TrimSpace(item.Text)
		if len([]rune(text)) > 400 {
			text = string([]rune(text)[:400]) + "…"
		}
		who := "They said"
		if item.Role == "assistant" {
			who = "You answered"
		}
		lines = append(lines, who+": "+text)
	}
	if len(lines) == 0 {
		return ""
	}
	if len(lines) > 6 {
		lines = lines[len(lines)-6:]
	}
	recap := strings.Join(lines, "\n")
	if len([]rune(recap)) > 1500 {
		recap = string([]rune(recap)[len([]rune(recap))-1500:])
	}
	return "[Earlier in your chat with this person:\n" + recap + "]\n\n"
}
