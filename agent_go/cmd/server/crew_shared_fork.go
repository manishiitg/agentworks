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
	"slices"
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
// threadCwd is the folder this turn's CLI runs in (from the agent itself), where the copy must land to be resumed.
func (api *StreamingAPI) forkOwnerSessionForSender(ctx context.Context, userID, sessionID, threadProvider, threadTransport, threadCwd string, ask *crewMacSharedAsk) (runtime *ChatHistoryAgentRuntime, recap string) {
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
	if threadProvider != "" && threadProvider != handle.Provider {
		return skip(fmt.Sprintf("this thread runs %s and the owner's chat %s", threadProvider, handle.Provider))
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
	// The CLI ran in the chat's runtime folder as the server computes it now (as Move online exports it); the saved
	// handle's folder can be from an older state root, so it is only the fallback.
	var bundle *sessiontransfer.Bundle
	cwds := []string{}
	if cwd, e := crewCLIWorkingDir(roots.Virtual, userID, owner.SessionID, handle.Provider, false); e == nil && cwd != "" {
		cwds = append(cwds, cwd)
	}
	if handle.WorkingDir != "" {
		cwds = append(cwds, handle.WorkingDir)
	}
	if handle.Provider == "claude-code" {
		cwds = append(cwds, claudeSessionWorkingDirs(store, handle.NativeSessionID, handle.WorkingDir)...)
	}
	for _, cwd := range cwds {
		if bundle, err = exportCrewNativeProgram(ctx, userID, runtimeDir, store, cwd, handle.NativeSessionID, handle.Provider, handle.Transport, out, false); err == nil && bundle != nil {
			break
		}
	}
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
	// The copy resumes in this thread's own CLI (the chat view's transport, not the owner's terminal).
	if threadTransport != "" {
		bundle.Transport = threadTransport
	}
	closeWorkflowPolicyCLI(sessionID, handle.Provider, "a shared agent's thread starts from its owner's latest conversation")
	forked, err := api.importCrewNativeInto(ctx, claims, roots, thread, sessionID, bundle, threadCwd)
	if err != nil || forked == nil {
		return skip("copy could not be imported: " + fmt.Sprint(err))
	}
	// It is this thread's session now: the same definition and tools as the turn about to run, so it is resumed and
	// not replaced by a fresh one.
	api.conversationMux.RLock()
	forked.AgentProfileKey = api.lastAgentProfileKeyBySession[sessionID]
	forked.AgentToolsMode = api.lastAgentToolsModeBySession[sessionID]
	api.conversationMux.RUnlock()
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

// claudeSessionWorkingDirs finds the folder Claude Code really ran a session in when the saved handle names an older one:
// Claude keeps <store>/projects/<slug of its folder>/<id>.jsonl, and the folder is one of the CLI runtime folders.
func claudeSessionWorkingDirs(store, id, saved string) []string {
	matches, _ := filepath.Glob(filepath.Join(store, "projects", "*", id+".jsonl"))
	if len(matches) == 0 {
		return nil
	}
	bases := []string{}
	if state, err := workflowCLIStateRoot(); err == nil {
		bases = append(bases, filepath.Join(state, "cli-runtimes", "v1"))
	}
	if saved != "" {
		bases = append(bases, filepath.Dir(saved))
	}
	// The app's own state folders on this Mac (the name has changed case over time).
	if config, err := os.UserConfigDir(); err == nil {
		for _, name := range []string{"agentworks", "AgentWorks"} {
			bases = append(bases, filepath.Join(config, name, "state", "cli-runtimes", "v1"))
		}
	}
	out := []string{}
	for _, match := range matches {
		slug := filepath.Base(filepath.Dir(match))
		for _, base := range bases {
			dirs, _ := filepath.Glob(filepath.Join(base, "*"))
			for _, dir := range dirs {
				if sessiontransfer.ClaudeSlug(dir) == slug && !slices.Contains(out, dir) {
					out = append(out, dir)
				}
			}
		}
	}
	return out
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
