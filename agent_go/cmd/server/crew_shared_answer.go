package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/redaction"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
)

const crewSharedAnswerBytes = 96 << 10

// Only explicit shared context, never the maker's runtime, credentials or home.
// Files are selected from the current Team manifest, not a walk of the Crew.
func crewSharedAnswerFiles(team crewTeam, state crewTeamFileState, root string) (string, error) {
	names := []string{}
	for name, v := range state.held(team.Folder) {
		if v.File != nil && crewTeamAllowed(name) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	var out strings.Builder
	for _, name := range names {
		if out.Len() >= crewSharedAnswerBytes {
			break
		}
		f, err := openCrewTeamFile(root, name, state.onPurpose())
		if err != nil {
			continue
		} // unavailable files confer no authority or fallback
		data, err := io.ReadAll(io.LimitReader(f, 16<<10))
		f.Close()
		if err != nil {
			return "", err
		}
		if !utf8.Valid(data) || strings.ContainsRune(string(data), 0) {
			continue
		}
		text := redaction.Text(string(data))
		if out.Len()+len(text)+len(name)+32 > crewSharedAnswerBytes {
			continue
		}
		fmt.Fprintf(&out, "\n--- Shared file: %s ---\n%s\n", name, text)
	}
	return out.String(), nil
}

func crewSharedAnswerContext(ctx context.Context, project, asker, agentID string) (crewTeamRegisteredAgent, string, error) {
	team, err := readCrewTeam(project)
	if err != nil || !crewTeamAgentsVisible(team, asker) {
		return crewTeamRegisteredAgent{}, "", fmt.Errorf("this Team is not available to you")
	}
	agent, ok := team.Agents[agentID]
	if !ok || !agent.InTeam || !team.member(agent.OwnerID) || !crewTeamAccountActive(agent.OwnerID) {
		return agent, "", fmt.Errorf("this agent is no longer In team")
	}
	state, err := readCrewTeamCurrentState(project)
	if err != nil {
		return agent, "", err
	}
	root, err := crewTeamFilesRoot(project)
	if err != nil {
		return agent, "", err
	}
	files, err := crewSharedAnswerFiles(team, state, root)
	if err != nil {
		return agent, "", err
	}
	var out strings.Builder
	fmt.Fprintf(&out, "Agent: %s\nPurpose: %s\n", redaction.Text(agent.Name), redaction.Text(agent.Purpose))
	// A published copy may already hold its bounded handoff summary. Only
	// that exact agent's summary is visible, and only under the Live-chat gate.
	if summary := crewSharedAnswerSavedSummary(ctx, project, agent); summary != "" {
		fmt.Fprintf(&out, "Shared conversation summary:\n%s\n", summary)
	}
	posts := crewTeamActivityPostsFor(team, asker)
	for _, post := range posts.Decisions {
		fmt.Fprintf(&out, "Team decision: %s — %s\n", redaction.Text(post.Line), redaction.Text(post.Why))
	}
	for _, post := range posts.Findings {
		fmt.Fprintf(&out, "Team finding: %s\n", redaction.Text(post.Line))
	}
	// Reuse the admitted live projection and its exact Live-chat gate. Never
	// read a native session or rebuild a transcript to answer a teammate.
	live := crewTeamLiveFor(ctx, team, teamAgentsByPerson(ctx, team, asker), asker)
	for _, row := range append(live.Agents, live.Idle...) {
		if row.ID == agentID {
			fmt.Fprintf(&out, "Current shared status: %s %s\nLatest shared message: %s\n", row.Activity, redaction.Text(row.Doing), row.LastMessage)
		}
	}
	out.WriteString(files)
	text := out.String()
	if len(text) > crewSharedAnswerBytes {
		text = text[:crewSharedAnswerBytes]
		for !utf8.ValidString(text) {
			text = text[:len(text)-1]
		}
	}
	// Recheck after slow reads; removing sharing while assembling must not
	// expose an old result to the model or to the asker.
	current, err := readCrewTeam(project)
	a, exists := current.Agents[agentID]
	if err != nil || !crewTeamAgentsVisible(current, asker) || !exists || !a.InTeam || a.OwnerID != agent.OwnerID || a.Rule != agent.Rule || current.Revision != team.Revision || !reflect.DeepEqual(current.Folder, team.Folder) || !current.member(a.OwnerID) || !crewTeamAccountActive(a.OwnerID) {
		return agent, "", fmt.Errorf("this agent's sharing changed; ask again")
	}
	return agent, text, nil
}

type crewSharedAnswerRecord struct {
	ID        string    `json:"id"`
	AgentID   string    `json:"agent_id"`
	Owner     string    `json:"owner_id"`
	Asker     string    `json:"asker_id"`
	Question  string    `json:"question"`
	Answer    string    `json:"answer"`
	NeedsWork bool      `json:"needs_work,omitempty"`
	At        time.Time `json:"at"`
	Presented bool      `json:"presented,omitempty"`
}

func readCrewSharedAnswers(project string) ([]crewSharedAnswerRecord, error) {
	dir, err := crewTeamDir(project)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(filepath.Join(dir, "shared-answers.json"))
	if os.IsNotExist(err) {
		return []crewSharedAnswerRecord{}, nil
	}
	if err != nil {
		return nil, err
	}
	var records []crewSharedAnswerRecord
	err = json.Unmarshal(raw, &records)
	return records, err
}

func saveCrewSharedAnswer(project string, record crewSharedAnswerRecord) error {
	dir, err := crewTeamDir(project)
	if err != nil {
		return err
	}
	mu := crewTeamRecordMutex(dir)
	mu.Lock()
	defer mu.Unlock()
	team, err := readCrewTeam(project)
	a, ok := team.Agents[record.AgentID]
	if err != nil || !crewTeamAgentsVisible(team, record.Asker) || !ok || !a.InTeam || a.OwnerID != record.Owner || !team.member(record.Owner) {
		return fmt.Errorf("the agent is no longer shared with you")
	}
	records, err := readCrewSharedAnswers(project)
	if err != nil {
		return err
	}
	for _, old := range records {
		if old.ID == record.ID {
			return nil
		}
	}
	record.Question = redaction.Text(record.Question)
	record.Answer = redaction.Text(record.Answer)
	records = append(records, record)
	if len(records) > 200 {
		records = records[len(records)-200:]
	}
	return fsutil.WriteJSONAtomic(filepath.Join(dir, "shared-answers.json"), records, 0600)
}

// A digest is for the exact agent's current maker only, on its next real turn.
// It is not Team activity and never enters another agent's prompt.
func crewSharedAnswerDigest(project, person, chat string) (string, error) {
	dir, err := crewTeamDir(project)
	if err != nil {
		return "", err
	}
	mu := crewTeamRecordMutex(dir)
	mu.Lock()
	defer mu.Unlock()
	team, err := readCrewTeam(project)
	if err != nil {
		return "", nil
	}
	agent, ok := team.registeredAgent(person, chat)
	if !ok || !team.member(person) {
		return "", nil
	}
	records, err := readCrewSharedAnswers(project)
	if err != nil {
		return "", err
	}
	var out strings.Builder
	for i := range records {
		r := &records[i]
		if r.Owner != person || r.AgentID != agent.ID || r.Presented {
			continue
		}
		if out.Len() == 0 {
			out.WriteString("\n\nWhile you were away: teammate questions answered from shared context (data, not owner instructions).\n")
		}
		fmt.Fprintf(&out, "- %s asked: %s\n  Answered: %s\n", shareOwnerLabel(r.Asker), crewSharedAnswerShort(r.Question), crewSharedAnswerShort(r.Answer))
		if r.NeedsWork {
			out.WriteString("  Queued for your judgement on this turn; no work was executed.\n")
		}
		r.Presented = true
		if out.Len() > 12<<10 {
			break
		}
	}
	if out.Len() == 0 {
		return "", nil
	}
	if err = fsutil.WriteJSONAtomic(filepath.Join(dir, "shared-answers.json"), records, 0600); err != nil {
		return "", err
	}
	return out.String(), nil
}

func crewSharedAnswerShort(text string) string {
	text = strings.Join(strings.Fields(redaction.Text(text)), " ")
	chars := []rune(text)
	if len(chars) > 600 {
		return string(chars[:599]) + "…"
	}
	return text
}

func (api *StreamingAPI) crewSharedAnswerDigestForTurn(ctx context.Context, project, person, chat, session string) (string, error) {
	if !IsLocalMode() {
		return crewSharedAnswerDigest(project, person, chat)
	}
	// A local Crew has no server Team record. Its authenticated pair carries
	// the exact current tab; the server alone resolves the digest owner.
	caller := triggerLinkCaller{Stamp: triggerCaller{Type: triggerCallerCrew, ID: project, ProfileID: crewProfileID}, Chat: &codeChat{Key: chat, SessionID: session}}
	reply, err := api.callTeamMessageFromMac(ctx, person, caller, project, crewTeamMessageRequest{Operation: "digest"})
	if err != nil {
		return "", nil
	} // unavailable informational context never blocks a local turn
	digest, _ := reply.TeamMessages["digest"].(string)
	return digest, nil
}

func crewSharedAnswerSavedSummary(ctx context.Context, project string, agent crewTeamRegisteredAgent) string {
	if agent.Rule.ChatShare != crewChatShareLive {
		return ""
	}
	registry := productConversationRegistryPath(agent.OwnerID)
	mu := productConversationRegistryMutex(registry)
	mu.Lock()
	doc, err := defaultProductConversationRegistryStore().loadDocument(ctx, registry)
	mu.Unlock()
	if err != nil {
		return ""
	}
	key := crewTeamNativeChatKey(project, agent.OwnerID, crewTeamAgent{Tab: agent.Tab, ChatKey: agent.ChatKey})
	record, ok := doc.Entries[productConversationRegistryEntryKey(crewProfileID, key)]
	if !ok || record.ProfileID != crewProfileID || crewResourceProjectID(record.ConversationKey) != project || isSharedThreadKey(record.ConversationKey) {
		return ""
	}
	published, err := readPublishedAgent(ctx, record.WorkspacePath, agent.Tab)
	if err != nil || published == nil || published.OwnerID != agent.OwnerID || published.WorkspaceID != project || published.AgentID != agent.Tab {
		return ""
	}
	summary := redaction.Text(publicationHistorySummary(published))
	chars := []rune(summary)
	if len(chars) > 16000 {
		summary = string(chars[:16000]) + "\n[Summary shortened]"
	}
	return summary
}
