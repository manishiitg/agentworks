package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Resolve only this signed-in person's real source record. A website cannot
// invent a Mac binding, read another person's journal or clone a home copy.
func registerCrewPutOnlineServerSource(ctx context.Context, person, project, key string) (string, ProductConversationRecord, error) {
	var source ProductConversationRecord
	t, err := readCrewTeamOrNew(project, person)
	if err != nil || !t.member(person) || t.RosterSource != "" || !crewTeamAccountActive(person) || ownerOfCrewProjectID(project) != t.OwnerID || !crewTeamAccountActive(t.OwnerID) {
		return "", source, errCrewTeamAgentNotYours
	}
	if row, ok := t.registeredAgent(person, key); ok {
		if row.Terminal != nil {
			return row.ID, source, nil
		}
		if row.Place == "mac" {
			return "", source, fmt.Errorf("put this source online from its Mac")
		}
		key = row.ChatKey
	}
	doc, err := defaultProductConversationRegistryStore().loadDocument(ctx, productConversationRegistryPath(person))
	if err != nil {
		return "", source, err
	}
	name, purpose := "", ""
	for _, r := range doc.Entries {
		if r.ProfileID != crewProfileID || r.WorkspacePath != crewProjectRootByID(project) || crewResourceProjectID(r.ConversationKey) != project || isSharedThreadKey(r.ConversationKey) || hiddenFromTabStrip(r.ConversationKey) {
			continue
		}
		tab := crewAgentPublishTab(r.ConversationKey)
		if tab == "" {
			continue
		}
		identity := doc.Agents[productConversationRegistryEntryKey(crewProfileID, r.ConversationKey)]
		if key != r.ConversationKey && key != tab && (key == "" || !strings.EqualFold(key, identity.Name)) {
			continue
		}
		if source.ConversationKey != "" {
			return "", source, fmt.Errorf("choose the source by its exact chat key")
		}
		source, name, purpose = r, identity.Name, identity.Purpose
	}
	b := crewOfflineBinding{Provider: "agentworks", Session: source.SessionID, Attachment: source.ConversationID}
	if !b.valid() {
		return "", source, fmt.Errorf("save this source conversation before putting it online")
	}
	if row, ok := t.registeredAgent(person, source.ConversationKey); ok {
		return row.ID, source, nil
	}
	private := false
	people := []crewTeamAgentsPerson{{UserID: person, Agents: []crewTeamAgent{{Tab: crewAgentPublishTab(source.ConversationKey), ChatKey: source.ConversationKey, Name: name, Purpose: purpose, Place: "server", initialInTeam: &private}}}}
	if err := assignCrewTeamAgentIDs(t, people); err != nil {
		return "", source, err
	}
	current, err := readCrewTeam(project)
	row, ok := current.registeredAgent(person, source.ConversationKey)
	if err != nil || !ok {
		return "", source, fmt.Errorf("this source changed")
	}
	log.Printf("[AGENT_OFFLINE_COPY] project=%s agent=%s actor=%s event=source_registered place=server", project, row.ID, person)
	return row.ID, source, nil
}

func crewOfflineServerSource(ctx context.Context, user string, record ProductConversationRecord, t crewTeam) (crewTeamRegisteredAgent, error) {
	a, ok := t.registeredAgent(user, record.ConversationKey)
	if !ok || a.Terminal != nil || a.Place == "mac" || a.OwnerID != user || t.RosterSource != "" || !t.member(user) || !crewTeamAccountActive(user) || ownerOfCrewProjectID(t.ProjectID) != t.OwnerID || !crewTeamAccountActive(t.OwnerID) {
		return a, fmt.Errorf("server source unavailable")
	}
	wanted, err := crewOfflineAppWanted(t, a)
	if err != nil || !wanted {
		return a, fmt.Errorf("server source not requested")
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

// The same coalesced finished-turn worker captures server sources. It writes
// only appended portable JSONL chunks, with a fresh source gate per commit.
func (api *StreamingAPI) syncCrewOfflineServerTurn(ctx context.Context, user, session, workspace string) error {
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
	a, err := crewOfflineServerSource(ctx, user, record, t)
	if err != nil {
		return nil
	} // ordinary private chats are not copied implicitly
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
