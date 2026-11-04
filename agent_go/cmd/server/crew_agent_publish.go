package server

// PLAT-856-523: publishing changes one tab's execution location. The Crew pair,
// other conversations and the owner's Mac folder retain their identities.
import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/projectactions"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

type crewAgentPublishSelection struct {
	Tab       string   `json:"tab"`
	Paths     []string `json:"paths"`
	AccountID string   `json:"account_id,omitempty"`
}

// This record is written only after owner/pair admission. Paths are derived
// locally from the stable tab, never supplied by the other computer.
type crewAgentPlacement struct {
	Name                  string                    `json:"name"`
	MacMainConversationID string                    `json:"mac_main_conversation_id,omitempty"`
	Tab                   string                    `json:"tab"`
	Location              string                    `json:"location"` // publishing or server
	Since                 string                    `json:"since,omitempty"`
	TransferID            string                    `json:"transfer_id"`
	SessionID             string                    `json:"session_id"`
	ConversationID        string                    `json:"conversation_id"`
	PairID                string                    `json:"pair_id"`
	SplitFiles            bool                      `json:"split_files"`
	Selection             crewAgentPublishSelection `json:"selection"`
}

var crewAgentPlacementMu sync.Mutex

func crewAgentPublishKey(project, tab string) string {
	if tab == macMainTab {
		return project
	}
	return project + codeChatSideMarker + tab
}

func crewAgentPublishTab(key string) string {
	if _, tab, ok := strings.Cut(key, codeChatSideMarker); ok {
		if strings.HasPrefix(tab, sharedThreadChatPrefix) {
			suffix := strings.TrimPrefix(tab, sharedThreadChatPrefix)
			if at := strings.LastIndex(suffix, "-"); at > 0 && len(suffix[at+1:]) == 12 {
				tab = suffix[:at]
			}
		}
		if crewMacTabID(tab) {
			return tab
		}
		return ""
	}
	if key != "" && !strings.Contains(key, ":") {
		return macMainTab
	}
	return ""
}

func crewAgentPlacementsPath(project string) (string, error) {
	p, err := crewSyncManifestPath(project)
	return p + ".placements", err
}

func readCrewAgentPlacements(project string) (map[string]crewAgentPlacement, error) {
	p, err := crewAgentPlacementsPath(project)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return map[string]crewAgentPlacement{}, nil
	}
	if err != nil {
		return nil, err
	}
	var out map[string]crewAgentPlacement
	if err = json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = map[string]crewAgentPlacement{}
	}
	return out, nil
}

func writeCrewAgentPlacement(project string, entry crewAgentPlacement) error {
	if !crewMacTabID(entry.Tab) || entry.PairID == "" || entry.TransferID == "" {
		return fmt.Errorf("invalid published agent")
	}
	crewAgentPlacementMu.Lock()
	defer crewAgentPlacementMu.Unlock()
	rows, err := readCrewAgentPlacements(project)
	if err != nil {
		return err
	}
	rows[entry.Tab] = entry
	p, err := crewAgentPlacementsPath(project)
	if err != nil {
		return err
	}
	return fsutil.WriteJSONAtomic(p, rows, 0600)
}

func crewPublishedAgent(project, tab string) (*crewAgentPlacement, error) {
	rows, err := readCrewAgentPlacements(project)
	if err != nil {
		return nil, err
	}
	entry, ok := rows[tab]
	if !ok {
		return nil, nil
	}
	b, err := crewroot.ReadSync(project)
	if err != nil {
		return nil, err
	}
	if b == nil || b.PairID != entry.PairID {
		return nil, fmt.Errorf("this agent's server connection changed; reopen Access")
	}
	return &entry, nil
}

func crewAgentWorkingPath(roots crewroot.Roots, entry crewAgentPlacement) string {
	area := "resources"
	if entry.SplitFiles {
		area = "files"
	}
	return roots.Virtual + "/published-agents/" + entry.Tab + "/" + area
}

func crewAgentRuntimeFolder(folder, user, session string) (string, error) {
	if IsLocalMode() {
		return folder, nil
	}
	roots, found, err := crewStorageRoots(folder)
	if err != nil || !found {
		return folder, err
	}
	rows, err := readCrewAgentPlacements(roots.ID)
	if err != nil || len(rows) == 0 {
		return folder, err
	}
	for _, entry := range rows {
		if entry.SessionID == session {
			return crewAgentWorkingPath(roots, entry), nil
		}
	}
	// Rotating a chat keeps its tab and working folder, but has a new session.
	doc, err := defaultProductConversationRegistryStore().loadDocument(context.Background(), productConversationRegistryPath(user))
	if err != nil {
		return folder, err
	}
	for _, record := range doc.Entries {
		if record.ProfileID == crewProfileID && record.SessionID == session && crewResourceProjectID(record.ConversationKey) == roots.ID {
			if entry, ok := rows[crewAgentPublishTab(record.ConversationKey)]; ok {
				return crewAgentWorkingPath(roots, entry), nil
			}
		}
	}
	return folder, nil
}

func crewAgentScopedRoots(roots crewroot.Roots, tab string) (crewroot.Roots, error) {
	if !crewMacTabID(tab) {
		return roots, fmt.Errorf("choose an agent in this Crew")
	}
	base, err := crewroot.ResolveContained(roots.Resources, "published-agents/"+tab)
	if err != nil {
		return roots, err
	}
	for _, sub := range []string{"files", "resources"} {
		p, err := crewroot.ResolveContained(base, sub)
		if err != nil {
			return roots, err
		}
		if err = os.MkdirAll(p, 0770); err != nil {
			return roots, err
		}
	}
	private := filepath.Join(roots.Private, "published-agents", tab)
	if err = os.MkdirAll(private, 0700); err != nil {
		return roots, err
	}
	roots.Files, roots.Resources, roots.Private = filepath.Join(base, "files"), filepath.Join(base, "resources"), private
	return roots, nil
}

func cleanCrewAgentPublishSelection(in crewAgentPublishSelection) (crewAgentPublishSelection, error) {
	if !crewMacTabID(in.Tab) || len(in.Paths) > 100 {
		return in, fmt.Errorf("choose an agent and up to 100 working folders or files")
	}
	out := []string{}
	for _, p := range in.Paths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if strings.ContainsAny(p, "\\\x00") || path.IsAbs(p) || path.Clean(p) != p || p == ".." || strings.HasPrefix(p, "../") {
			return in, fmt.Errorf("choose files inside this Crew, using paths relative to its folder")
		}
		if !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	in.Paths = out
	return in, nil
}

// Resources needed to understand the current agent travel even when no working
// files were selected. All content still passes crewsync's private-file rules.
func crewAgentPublishIncludes(selection crewAgentPublishSelection, key string, directory bool) bool {
	area, rel, _ := strings.Cut(key, "/")
	if area == "knowledge" {
		return false
	}
	if area != "resources" && area != "files" {
		return false
	}
	if rel == "published-agents" || strings.HasPrefix(rel, "published-agents/") {
		return false
	}
	paths := selection.Paths
	if area == "resources" {
		paths = append(slices.Clone(paths), "skills", "memory", "MEMORY.md", "PROJECT_INSTRUCTIONS.md", "AGENTS.md", "CLAUDE.md")
	}
	for _, p := range paths {
		if p == "." || rel == p || strings.HasPrefix(rel, p+"/") || directory && strings.HasPrefix(p, rel+"/") {
			return true
		}
	}
	return false
}

func crewAgentTargetMatches(target string, h crewMoveHeader) bool {
	if target == "" || target == "main" || target == h.MainKey || target == h.SourceID {
		return h.AgentTab == macMainTab
	}
	return target == h.AgentTab || target == crewAgentPublishKey(h.SourceID, h.AgentTab) || !strings.Contains(target, ":") && crewAgentChatID(target) == h.AgentTab
}

func crewAgentScheduleMatches(s projectactions.Schedule, h crewMoveHeader) bool {
	var input scheduleExecutionInput
	if json.Unmarshal(s.Input, &input) == nil && input.Condition != nil {
		return crewAgentTargetMatches(input.Condition.Target.ChatID, h)
	}
	return s.Action.Type == "message" && crewAgentTargetMatches(s.Action.Tab, h)
}

func filterCrewAgentCallers(h *crewMoveHeader) error {
	h.Schedules = slices.DeleteFunc(h.Schedules, func(s projectactions.Schedule) bool { return !crewAgentScheduleMatches(s, *h) })
	h.Functions = slices.DeleteFunc(h.Functions, func(s projectFunctionDefinition) bool {
		return s.Action.Type != "message" || !crewAgentTargetMatches(s.Action.Tab, *h)
	})
	h.Webhooks = slices.DeleteFunc(h.Webhooks, func(s projectWebhookDefinition) bool { return !crewAgentTargetMatches(s.Action.Tab, *h) })
	h.Callers = slices.DeleteFunc(h.Callers, func(s crewMoveCaller) bool {
		return s.Definition.Schedule.Condition == nil || !crewAgentTargetMatches(s.Definition.Schedule.Condition.Target.ChatID, *h)
	})
	ids := map[string]bool{}
	for _, s := range h.Schedules {
		ids[s.ID] = true
	}
	for id := range h.Watchers {
		if !ids[id] {
			delete(h.Watchers, id)
		}
	}
	for _, s := range h.Schedules {
		if s.When.After != nil {
			for _, id := range s.When.After.Schedules {
				if !ids[id] {
					return fmt.Errorf("%s depends on another agent's schedule; publish the Crew to move them together", s.Name)
				}
			}
		}
	}
	return nil
}

// Publishing the remaining Crew later does not re-import the retired Mac copy
// of an agent or replace that agent's live server callers with disabled copies.
func excludePublishedCrewCallers(h *crewMoveHeader, published map[string]crewAgentPlacement) {
	moved := func(target string) bool {
		for tab := range published {
			match := *h
			match.AgentTab = tab
			if crewAgentTargetMatches(target, match) {
				return true
			}
		}
		return false
	}
	h.Schedules = slices.DeleteFunc(h.Schedules, func(s projectactions.Schedule) bool {
		var input scheduleExecutionInput
		if json.Unmarshal(s.Input, &input) == nil && input.Condition != nil {
			return moved(input.Condition.Target.ChatID)
		}
		return s.Action.Type == "message" && moved(s.Action.Tab)
	})
	h.Functions = slices.DeleteFunc(h.Functions, func(s projectFunctionDefinition) bool { return s.Action.Type == "message" && moved(s.Action.Tab) })
	h.Webhooks = slices.DeleteFunc(h.Webhooks, func(s projectWebhookDefinition) bool { return moved(s.Action.Tab) })
	h.Callers = slices.DeleteFunc(h.Callers, func(s crewMoveCaller) bool {
		return s.Definition.Schedule.Condition != nil && moved(s.Definition.Schedule.Condition.Target.ChatID)
	})
	ids := map[string]bool{}
	for _, s := range h.Schedules {
		ids[s.ID] = true
	}
	for id := range h.Watchers {
		if !ids[id] {
			delete(h.Watchers, id)
		}
	}
}

func mergeCrewPublishedItems[T any](old, incoming []T, id func(T) string) []T {
	for _, item := range incoming {
		key := id(item)
		old = slices.DeleteFunc(old, func(v T) bool { return id(v) == key })
		old = append(old, item)
	}
	return old
}

func (api *StreamingAPI) crewAgentPublishRecord(ctx context.Context, claims *UserClaims, roots crewroot.Roots, tab string) (ProductConversationRecord, ProductAgentIdentity, error) {
	doc, err := defaultProductConversationRegistryStore().loadDocument(ctx, productConversationRegistryPath(claims.UserID))
	if err != nil {
		return ProductConversationRecord{}, ProductAgentIdentity{}, err
	}
	key := productConversationRegistryEntryKey(crewProfileID, crewAgentPublishKey(roots.ID, tab))
	record, ok := doc.Entries[key]
	identity := doc.Agents[key]
	if tab == macMainTab {
		if main, ok := readCrewMainIdentity(ctx, roots.Virtual); ok {
			identity.Name = "Main"
			identity.Purpose = firstNonEmptyTrimmed(main.Purpose, identity.Purpose)
			identity.Icon = firstNonEmptyTrimmed(main.Icon, identity.Icon)
		}
	}
	if !ok || canonicalCrewWorkspaceRoot(record.WorkspacePath) != roots.Virtual || record.ProfileID != crewProfileID || hiddenFromTabStrip(record.ConversationKey) {
		return record, identity, fmt.Errorf("choose one of your Crew's agents")
	}
	if (tab != macMainTab && strings.TrimSpace(identity.Name) == "") || strings.TrimSpace(identity.Purpose) == "" || strings.TrimSpace(identity.Icon) == "" {
		return record, identity, fmt.Errorf("give this agent a name, purpose and icon before publishing; ask it to set its identity")
	}
	if api.crewAgentTurnRunning(record.SessionID) {
		return record, identity, fmt.Errorf("finish or stop this agent's reply before publishing")
	}
	return record, identity, nil
}
