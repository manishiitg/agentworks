package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func crewOfflineServerOwner(ctx context.Context, user string, record ProductConversationRecord, t crewTeam) (crewTeamRegisteredAgent, error) {
	a, ok := t.registeredAgent(user, record.ConversationKey)
	if !ok || a.Terminal != nil || a.Place == "mac" || a.OwnerID != user || t.RosterSource != "" || crewOfflineSourceOwner(t, user, a) != nil || !crewTeamAccountActive(user) || ownerOfCrewProjectID(t.ProjectID) != t.OwnerID || !crewTeamAccountActive(t.OwnerID) {
		return a, fmt.Errorf("server source unavailable")
	}
	doc, err := defaultProductConversationRegistryStore().loadDocument(ctx, productConversationRegistryPath(user))
	if err != nil {
		return a, err
	}
	current, ok := doc.Entries[productConversationRegistryEntryKey(crewProfileID, record.ConversationKey)]
	if !ok || current.SessionID != record.SessionID || current.ConversationID != record.ConversationID || current.WorkspacePath != record.WorkspacePath || current.ProfileID != record.ProfileID {
		return a, fmt.Errorf("server source binding changed")
	}
	return a, nil
}

func crewOfflineServerSource(ctx context.Context, user string, record ProductConversationRecord, t crewTeam) (crewTeamRegisteredAgent, error) {
	a, err := crewOfflineServerOwner(ctx, user, record, t)
	if err != nil {
		return a, err
	}
	wanted, err := crewOfflineAppWanted(t, a, crewOfflineBinding{Provider: "agentworks", Session: record.SessionID, Attachment: record.ConversationID})
	if err != nil || !wanted {
		return a, fmt.Errorf("server source not requested")
	}
	return a, nil
}

// The same coalesced finished-turn worker captures server sources. It writes
// only appended portable JSONL chunks, with a fresh source gate per commit.
func (api *StreamingAPI) syncCrewOfflineServerTurn(ctx context.Context, user, session, workspace string) (retErr error) {
	records, err := defaultProductConversationRegistryStore().crewWorkspaceChats(ctx, user, workspace)
	if err != nil {
		return err
	}
	var record ProductConversationRecord
	for _, r := range records {
		if r.ProfileID == crewProfileID && r.SessionID == session && r.WorkspacePath == workspace && !isSharedThreadKey(r.ConversationKey) && !hiddenFromTabStrip(r.ConversationKey) {
			if record.ConversationKey != "" {
				return fmt.Errorf("server source ambiguous")
			}
			record = r
		}
	}
	if record.ConversationKey == "" {
		return nil
	}
	project := crewResourceProjectID(record.ConversationKey)
	t, err := readCrewTeam(project)
	if err != nil || t.RosterSource != "" {
		return nil
	}
	// Return receipts use exact ownership even if sync selection is now off.
	// This never provisions a home or captures an otherwise private source.
	if _, e := crewOfflineServerOwner(ctx, user, record, t); e == nil {
		if _, e := crewOfflineCloudHandback(ctx, user, record); e != nil {
			log.Printf("[AGENT_OFFLINE_COPY] project=%s session=%s event=handback_skipped reason=cloud_source_unavailable", project, session)
		}
	}
	a, err := crewOfflineServerSource(ctx, user, record, t)
	if err != nil {
		return nil
	} // ordinary private chats are not copied implicitly
	stage := "history"
	log.Printf("[AGENT_OFFLINE_COPY] project=%s agent=%s session=%s event=server_capture_started", project, a.ID, session)
	defer func() {
		if retErr != nil {
			log.Printf("[AGENT_OFFLINE_COPY] project=%s agent=%s session=%s event=server_capture_failed stage=%s", project, a.ID, session, stage)
		} else {
			log.Printf("[AGENT_OFFLINE_COPY] project=%s agent=%s session=%s event=server_capture_finished", project, a.ID, session)
		}
	}()
	roots, found, err := crewStorageRoots(workspace)
	if err != nil || !found || roots.ID != project || roots.Owner != t.OwnerID {
		return fmt.Errorf("source Crew changed")
	}
	raw, err := ReadChatHistoryConversation(user, session, workspace)
	if err != nil {
		return err
	}
	var saved struct {
		UserID  string                    `json:"user_id"`
		Session string                    `json:"session_id"`
		History []llmtypes.MessageContent `json:"conversation_history"`
	}
	if json.Unmarshal(raw, &saved) != nil || saved.UserID != user || saved.Session != session {
		return fmt.Errorf("source history owner changed")
	}
	stage = "journal"
	path, err := crewOfflineAppJournal(roots, record, saved.History)
	if err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	b := crewOfflineBinding{Provider: "agentworks", Session: record.SessionID, Attachment: record.ConversationID}
	dir, err := crewOfflineDirectory(project, a.ID)
	if err != nil {
		return err
	}
	teamDir, err := crewTeamDir(project)
	if err != nil {
		return err
	}
	stage = "delta_commit"
	mu := crewTeamRecordMutex(teamDir)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		mu.Lock()
		current, e := readCrewTeam(project)
		if e == nil {
			_, e = crewOfflineServerSource(ctx, user, record, current)
		}
		if e != nil {
			mu.Unlock()
			return e
		}
		h, e := readCrewOfflineHistory(dir, b)
		if e != nil {
			mu.Unlock()
			return e
		}
		if h.Offset > st.Size() {
			mu.Unlock()
			return fmt.Errorf("source journal shrank")
		}
		got, e := crewOfflineFileTailHash(f, h.Offset)
		if e != nil || got != h.TailHash {
			mu.Unlock()
			return fmt.Errorf("source journal prefix changed")
		}
		if h.Offset == st.Size() {
			mu.Unlock()
			break
		}
		data := make([]byte, min(int64(crewOfflineDeltaLimit), st.Size()-h.Offset))
		_, e = f.ReadAt(data, h.Offset)
		if e != nil {
			mu.Unlock()
			return e
		}
		end := bytes.LastIndexByte(data, '\n') + 1
		if end == 0 {
			mu.Unlock()
			return fmt.Errorf("one source message exceeds delta capacity")
		}
		data = data[:end]
		version, e := chunkReader(bytes.NewReader(data), int64(len(data)), false, func(id string, data []byte) error { return putChunk(filepath.Join(dir, "chunks"), id, data) })
		if e == nil {
			_, e = commitCrewOfflineDelta(project, a.ID, dir, h, crewOfflineDelta{crewOfflineBinding: b, BaseRevision: h.Revision, Offset: h.Offset, PreviousTailHash: h.TailHash, Delta: version, SourceSize: st.Size()})
		}
		mu.Unlock()
		if e != nil {
			return e
		}
	}
	go api.refreshCrewOfflineCopy(project, a.ID)
	return nil
}
