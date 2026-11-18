package server

import (
	"bufio"
	"encoding/json"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
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
	if strings.HasPrefix(a.OwnerID, "team-") || a.Place == "server" {
		return crewAgentCopyRefusal("Fork a teammate's Mac agent, not the Team agent.")
	}
	return crewOfflineSourceOwner(t, a.OwnerID, a)
}

// Caller holds the Team record lock. Every byte comes from validated immutable
// source batches. The maker's Mac and the offline responder are never read.
func (api *StreamingAPI) selfForkCrewAgent(t crewTeam, viewer, agentID string, now time.Time) (offer crewAgentFork, err error) {
	a, ok := t.Agents[agentID]
	if !ok {
		return offer, crewAgentCopyRefusal("This agent is no longer available.")
	}
	if err = crewAgentSelfForkAdmit(t, viewer, a); err != nil {
		return offer, err
	}
	if !crewAgentForkRunsOnMac(t, viewer) {
		return offer, crewAgentCopyRefusal("Connect your own Mac to this Crew before forking an agent.")
	}
	ref, ready, err := crewOfflineCopyReady(t, a)
	if err != nil || !ready {
		return offer, crewAgentCopyRefusal("No complete saved copy is available yet. Ask the maker to send you a copy.")
	}
	dir, err := crewOfflineDirectory(t.ProjectID, a.ID)
	if err != nil {
		return offer, err
	}
	h, err := readCrewOfflineHistory(dir, ref.Binding)
	if err != nil {
		return offer, err
	}
	if h.SourceSize <= 0 || h.SourceSize > 64<<20 {
		return offer, crewAgentCopyRefusal("This saved copy is too large to fork. Ask the maker to send you a copy.")
	}
	info, err := os.Stat(filepath.Join(dir, "history.json"))
	if err != nil {
		return offer, err
	}
	offer, err = newCrewAgentFork(t, a.OwnerID, viewer, a.ID, now)
	if err != nil {
		return offer, err
	}
	// A copy carries public identity, never the maker's audience, ask rule,
	// native attachment or Mac pair. Revalidation reads the current source row.
	offer.Agent = crewTeamRegisteredAgent{ID: a.ID, OwnerID: a.OwnerID, Tab: a.Tab, ChatKey: a.ChatKey, Name: a.Name, Purpose: a.Purpose, Place: a.Place}
	offer.SelfFork = true
	offer.SourceBinding = &ref.Binding
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
	if ref.Binding.Provider == "agentworks" {
		scanner := bufio.NewScanner(raw)
		scanner.Buffer(make([]byte, 64<<10), crewOfflineDeltaLimit)
		for scanner.Scan() {
			var m llmtypes.MessageContent
			if err = json.Unmarshal(scanner.Bytes(), &m); err != nil {
				return offer, err
			}
			payload.Conversation.History = append(payload.Conversation.History, m)
		}
		if err = scanner.Err(); err != nil {
			return offer, err
		}
	} else {
		program, format, supported := crewOfflineNativeFormat(ref.Binding.Provider)
		if !supported {
			return offer, crewAgentCopyRefusal("This program cannot be forked yet.")
		}
		native, err := stageCrewOfflineNativeBundle(raw, ref.Binding, program, format, stage)
		if err != nil {
			return offer, err
		}
		payload.Conversation.Native = native
		// The complete native context travels independently. Match the existing
		// bounded native-history reader for visible dialogue / other-program resume.
		rows, _, err := readRecentNativeTranscript(filepath.Join(native.Root, "session.jsonl"), program, time.Time{})
		if err != nil {
			return offer, err
		}
		for _, m := range rows {
			if text := builderConversationMessageText(m); text != "" {
				payload.Conversation.History = append(payload.Conversation.History, llmtypes.TextPart(llmtypes.ChatMessageType(m.Role), text))
			}
		}
		chunks, err := crewAgentForkChunkStore(offer)
		if err != nil {
			return offer, err
		}
		payload.NativeFiles, _, err = crewNativeFiles(native.Root, native, false, func(id string, data []byte) error { return putChunk(chunks, id, data) })
		if err != nil {
			return offer, err
		}
	}
	// Verify the exact captured byte count; incomplete batches are not a copy.
	if st, e := raw.Stat(); e != nil || st.Size() != h.Offset {
		return offer, crewAgentCopyRefusal("The saved source is incomplete. Try again after it finishes syncing.")
	}
	if err = stageCrewAgentFork(t, offer, a.OwnerID, payload, now); err != nil {
		return offer, err
	}
	offer.Ready = true
	log.Printf("[AGENT_FORK] project=%s source=%s copy=%s actor=%s event=self_fork snapshot_at=%s", t.ProjectID, a.ID, offer.CopyAgentID, viewer, offer.SnapshotAt.Format(time.RFC3339))
	// The caller already holds the Team mutex that guards the activity record.
	if err = appendCrewTeamForkPostLocked(t.ProjectID, crewTeamActivityPost{ID: uuid.NewString(), Kind: "task", Event: "forked", Line: a.Name, OwnerID: viewer, ChatKey: crewWorkPersonPrefix + viewer, Agent: crewPersonShortName(viewer), At: now}); err != nil {
		return offer, err
	}
	publishCrewPanelChanged(t.ProjectID)
	return offer, nil
}
