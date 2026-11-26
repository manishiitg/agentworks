package server

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/claudecode"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/codexcli"
)

type crewAgentCopyRefusal string

func (e crewAgentCopyRefusal) Error() string { return string(e) }

// Recipient-initiated export is a read of the maker's shared conversation,
// never of the virtual responder or of another asker's private thread.
func crewAgentSelfForkAdmit(t crewTeam, viewer string, a crewTeamRegisteredAgent) error {
	if a.ID == "" || a.OwnerID == viewer || !t.On || !t.member(viewer) || !t.member(a.OwnerID) || !crewTeamSourceRowAdmits(t, viewer, a) {
		return crewAgentCopyRefusal("This agent is not shared with you now.")
	}
	if a.Rule.ChatShare != crewChatShareLive {
		return crewAgentCopyRefusal("This chat is hidden. You can't fork it.")
	}
	if a.Place == "server" {
		return crewAgentCopyRefusal("Fork a teammate's Mac agent.")
	}
	return crewOfflineSourceOwner(t, a.OwnerID, a)
}

// Caller holds the Team record lock. Both ways of copying a teammate's agent read the server's synced copy of the
// shared chat (#3218, owner 19 Nov: an agent goes to a teammate only once it is shared): the recipient pulling it
// (Fork to my Mac, self=true) and its maker sending it (self=false). Every byte comes from validated immutable source
// batches. The maker's Mac and the offline responder are never read.
func (api *StreamingAPI) selfForkCrewAgent(t crewTeam, viewer, agentID string, now time.Time) (crewAgentFork, error) {
	a, ok := t.Agents[agentID]
	if !ok {
		return crewAgentFork{}, crewAgentCopyRefusal("This agent is no longer available.")
	}
	if err := crewAgentSelfForkAdmit(t, viewer, a); err != nil {
		return crewAgentFork{}, err
	}
	if !crewAgentForkRunsOnMac(t, viewer) {
		return crewAgentFork{}, crewAgentCopyRefusal("Connect your own Mac to this project before forking an agent.")
	}
	return api.forkCrewAgentFromSyncedCopy(t, a, viewer, true, now)
}

// sendCrewAgentFromSyncedCopy is the maker's Send a copy: the same synced copy, offered to a teammate it is shared with.
func (api *StreamingAPI) sendCrewAgentFromSyncedCopy(t crewTeam, maker, recipient, agentID string, now time.Time) (crewAgentFork, error) {
	a, ok := t.Agents[agentID]
	if !ok || a.OwnerID != maker {
		return crewAgentFork{}, crewAgentCopyRefusal("Choose one of your own agents to send.")
	}
	if recipient == maker || !t.member(recipient) {
		return crewAgentFork{}, crewAgentCopyRefusal("Choose another person in this Team.")
	}
	if _, synced := crewOfflineRowBinding(t, a); !synced || !crewTeamSourceRowAdmits(t, recipient, a) {
		return crewAgentFork{}, crewAgentCopyRefusal("Share this agent with " + crewPersonShortName(recipient) + " in Team first, then send the copy.")
	}
	return api.forkCrewAgentFromSyncedCopy(t, a, recipient, false, now)
}

func (api *StreamingAPI) forkCrewAgentFromSyncedCopy(t crewTeam, a crewTeamRegisteredAgent, recipient string, self bool, now time.Time) (offer crewAgentFork, err error) {
	binding, ok := crewOfflineRowBinding(t, a)
	if !ok {
		return offer, crewAgentCopyRefusal("This agent's chat is not on the server yet. It is copied after sharing; try again in a minute.")
	}
	dir, err := crewOfflineDirectory(t.ProjectID, a.ID)
	if err != nil {
		return offer, err
	}
	h, err := readCrewOfflineHistory(dir, binding)
	if err != nil {
		return offer, err
	}
	if h.Revision == 0 || h.SourceSize <= 0 || h.Offset != h.SourceSize {
		done := 0
		if h.SourceSize > 0 {
			done = int(h.Offset * 100 / h.SourceSize)
		}
		log.Printf("[AGENT_FORK] project=%s source=%s event=refused reason=sync_incomplete offset=%d source_bytes=%d", t.ProjectID, a.ID, h.Offset, h.SourceSize)
		return offer, crewAgentCopyRefusal(fmt.Sprintf("This agent's chat is still being copied to the server (%d%% done). Try again in a minute.", done))
	}
	info, err := os.Stat(filepath.Join(dir, "history.json"))
	if err != nil {
		return offer, err
	}
	offer, err = newCrewAgentFork(t, a.OwnerID, recipient, a.ID, now)
	if err != nil {
		return offer, err
	}
	// A copy carries public identity, never the maker's audience, ask rule,
	// native attachment or Mac pair. Revalidation reads the current source row.
	offer.Agent = crewTeamRegisteredAgent{ID: a.ID, OwnerID: a.OwnerID, Tab: a.Tab, ChatKey: a.ChatKey, Name: a.Name, Purpose: a.Purpose, Place: a.Place}
	offer.SelfFork = self
	offer.SourceBinding = &binding
	offer.SnapshotAt = info.ModTime().UTC()
	if err = saveCrewAgentFork(offer); err != nil {
		return offer, err
	}
	defer func() {
		if err != nil {
			_ = removeCrewAgentFork(offer)
		}
	}()
	stage, err := crewAgentForkDirectory(t.ProjectID, offer.ID)
	if err != nil {
		return offer, err
	}
	rawPath := filepath.Join(stage, "source.jsonl")
	raw, err := os.OpenFile(rawPath, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return offer, err
	}
	defer os.Remove(rawPath)
	defer raw.Close()
	for _, batch := range h.Batches {
		if err = assembleChunked(filepath.Join(dir, "chunks"), batch, raw); err != nil {
			return offer, err
		}
	}
	if _, err = raw.Seek(0, io.SeekStart); err != nil {
		return offer, err
	}
	payload := crewAgentForkPayload{Conversation: &crewHandoffConversation{ConversationID: "conversation-" + uuid.NewString()}}
	trimmed := false
	if binding.Provider == "agentworks" {
		payload.Conversation.History, trimmed, err = readCrewForkJournalTail(raw, crewHandoffHistoryLimit-(1<<20))
		if err != nil {
			return offer, err
		}
	} else {
		program, format, supported := crewOfflineNativeFormat(binding.Provider)
		if !supported {
			return offer, crewAgentCopyRefusal("This program cannot be forked yet.")
		}
		native, err := stageCrewOfflineNativeBundle(raw, binding, program, format, stage)
		if err != nil {
			return offer, err
		}
		// #3140: the copy carries the whole chat, tool calls and results included, so a person on another program
		// starts with the full history. A session over the copy limit keeps only its recent saved messages.
		transcript := filepath.Join(native.Root, "session.jsonl")
		if h.SourceSize <= crewHandoffHistoryLimit {
			read := claudecode.ReadTranscriptFile
			if program == "codex-cli" {
				read = codexcli.ReadTranscriptFile
			}
			if payload.Conversation.History, err = read(transcript); err != nil {
				return offer, err
			}
		} else {
			rows, _, err := readRecentNativeTranscript(transcript, program, time.Time{})
			if err != nil {
				return offer, err
			}
			for _, m := range rows {
				if text := builderConversationMessageText(m); text != "" {
					payload.Conversation.History = append(payload.Conversation.History, llmtypes.TextPart(llmtypes.ChatMessageType(m.Role), text))
				}
			}
		}
		// A native session over the copy limit stays behind: the copy continues from its recent saved messages.
		if h.SourceSize > crewHandoffHistoryLimit {
			trimmed = true
		} else {
			payload.Conversation.Native = native
			chunks, err := crewAgentForkChunkStore(offer)
			if err != nil {
				return offer, err
			}
			payload.NativeFiles, _, err = crewNativeFiles(native.Root, native, false, func(id string, data []byte) error { return putChunk(chunks, id, data) })
			if err != nil {
				return offer, err
			}
		}
	}
	if trimmed {
		note := "This copy has the newest messages; older ones weren't copied because the chat is very large."
		payload.Conversation.History = append([]llmtypes.MessageContent{llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, note)}, payload.Conversation.History...)
	}
	// Verify the exact captured byte count; incomplete batches are not a copy.
	if st, e := raw.Stat(); e != nil || st.Size() != h.Offset {
		return offer, crewAgentCopyRefusal("The saved source is incomplete. Try again after it finishes syncing.")
	}
	if err = stageCrewAgentFork(t, offer, a.OwnerID, payload, now); err != nil {
		return offer, err
	}
	offer.Ready = true
	event := "sent_from_synced_copy"
	if self {
		event = "self_fork"
	}
	log.Printf("[AGENT_FORK] project=%s source=%s copy=%s recipient=%s event=%s source_bytes=%d trimmed=%t snapshot_at=%s", t.ProjectID, a.ID, offer.CopyAgentID, recipient, event, h.SourceSize, trimmed, offer.SnapshotAt.Format(time.RFC3339))
	// The caller already holds the Team mutex that guards the activity record.
	if err = appendCrewTeamForkPostLocked(t.ProjectID, crewTeamActivityPost{ID: uuid.NewString(), Kind: "task", Event: "forked", Line: a.Name, OwnerID: recipient, ChatKey: crewWorkPersonPrefix + recipient, Agent: crewPersonShortName(recipient), At: now}); err != nil {
		return offer, err
	}
	publishCrewPanelChanged(t.ProjectID)
	return offer, nil
}

// readCrewForkJournalTail keeps the newest whole messages of a synced app journal that fit in limit bytes, starting at
// a person's message so the copy never opens on a reply or tool result whose request was cut off (#3218, owner 19
// Nov: "above that size limit .. we still need to transfer what we can").
func readCrewForkJournalTail(raw *os.File, limit int64) ([]llmtypes.MessageContent, bool, error) {
	var sizes []int64
	scanner := bufio.NewScanner(raw)
	scanner.Buffer(make([]byte, 64<<10), crewOfflineDeltaLimit)
	for scanner.Scan() {
		sizes = append(sizes, int64(len(scanner.Bytes()))+1)
	}
	if err := scanner.Err(); err != nil {
		return nil, false, err
	}
	first, total := len(sizes), int64(0)
	for first > 0 && total+sizes[first-1] <= limit {
		first--
		total += sizes[first]
	}
	trimmed := first > 0
	if _, err := raw.Seek(0, io.SeekStart); err != nil {
		return nil, false, err
	}
	var out []llmtypes.MessageContent
	scanner = bufio.NewScanner(raw)
	scanner.Buffer(make([]byte, 64<<10), crewOfflineDeltaLimit)
	for line := 0; scanner.Scan(); line++ {
		if line < first {
			continue
		}
		var m llmtypes.MessageContent
		if err := json.Unmarshal(scanner.Bytes(), &m); err != nil {
			return nil, false, err
		}
		if trimmed && len(out) == 0 && !crewForkPersonMessage(m) {
			continue
		}
		out = append(out, m)
	}
	return out, trimmed, scanner.Err()
}

func crewForkPersonMessage(m llmtypes.MessageContent) bool {
	if m.Role != llmtypes.ChatMessageTypeHuman {
		return false
	}
	for _, part := range m.Parts {
		if _, ok := part.(llmtypes.TextContent); ok {
			return true
		}
	}
	return false
}
