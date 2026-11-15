package server

// PLAT-746-1024: one-off conversion of the former person-slot placements.
// The placed tab is its own source; names never link it to an original tab.
import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
)

type crewOnlineConversion struct {
	Agent          string `json:"agent_id"`
	Tab            string `json:"tab"`
	Ready          bool   `json:"ready"`
	FilesKeptAside int    `json:"files_kept_aside"`
}

// This is a one-off signed-in owner action, not an alternate publish transport.
// A changed old pair is not authority: actual saved conversation ownership is.
func (api *StreamingAPI) handleCrewConvertOnline(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	project := mux.Vars(r)["project"]
	if IsLocalMode() || claims == nil || claims.AccessToken != nil || claims.Scope != "" || ownerOfCrewProjectID(project) != claims.UserID || !crewTeamAccountActive(claims.UserID) {
		externalError(w, 403, "conversion_forbidden", "Only this Crew's signed-in owner can convert its old online agents.")
		return
	}
	rows, err := readCrewAgentPlacements(project)
	if err != nil {
		externalError(w, 409, "conversion_unavailable", "The old online records could not be read.")
		return
	}
	tabs := make([]string, 0, len(rows))
	for tab := range rows {
		tabs = append(tabs, tab)
	}
	sort.Strings(tabs)
	out := make([]crewOnlineConversion, 0, len(tabs))
	for _, tab := range tabs {
		result, e := api.convertCrewOnlinePlacement(r.Context(), claims.UserID, project, rows[tab])
		if e != nil {
			log.Printf("[AGENT_OFFLINE_COPY] project=%s tab=%s event=conversion_refused reason=source_or_storage_changed", project, tab)
			externalError(w, 409, "conversion_unavailable", "An online agent could not be converted; its source and backup are kept.")
			return
		}
		out = append(out, result)
	}
	externalJSON(w, map[string]any{"agents": out, "note": "Old selected working files are kept aside, not mounted. Share them through Team Files if the copy needs them."})
}

func crewOnlinePlacementRecord(ctx context.Context, person, project string, placement crewAgentPlacement) (ProductConversationRecord, error) {
	var empty ProductConversationRecord
	if !crewMacTabID(placement.Tab) || placement.Location != "server" || placement.SessionID == "" || placement.ConversationID == "" {
		return empty, fmt.Errorf("incomplete old placement")
	}
	doc, err := defaultProductConversationRegistryStore().loadDocument(ctx, productConversationRegistryPath(person))
	if err != nil {
		return empty, err
	}
	r, ok := doc.Entries[productConversationRegistryEntryKey(crewProfileID, crewAgentPublishKey(project, placement.Tab))]
	if !ok || r.ConversationKey != crewAgentPublishKey(project, placement.Tab) || r.ProfileID != crewProfileID || r.WorkspacePath != crewProjectRootByID(project) || r.SessionID != placement.SessionID || r.ConversationID != placement.ConversationID || isSharedThreadKey(r.ConversationKey) {
		return empty, fmt.Errorf("old source binding changed")
	}
	return r, nil
}

func (api *StreamingAPI) convertCrewOnlinePlacement(ctx context.Context, person, project string, placement crewAgentPlacement) (crewOnlineConversion, error) {
	out := crewOnlineConversion{Tab: placement.Tab}
	log.Printf("[AGENT_OFFLINE_COPY] project=%s tab=%s actor=%s event=conversion_started", project, placement.Tab, person)
	record, err := crewOnlinePlacementRecord(ctx, person, project, placement)
	if err != nil {
		return out, err
	}
	roots, found, err := crewStorageRoots(record.WorkspacePath)
	if err != nil || !found || roots.Owner != person {
		return out, fmt.Errorf("source roots changed")
	}
	// Keep a plain private backup before changing audience or retiring execution.
	backup := filepath.Join(roots.Private, "before-online-conversion", uuid.NewString())
	if err = os.MkdirAll(backup, 0700); err != nil {
		return out, err
	}
	original, err := readCrewAgentPlacements(project)
	if err != nil {
		return out, err
	}
	if current, ok := original[placement.Tab]; !ok || !sameCrewOnlinePlacement(current, placement) { // compared below without trusting a new pair
		return out, fmt.Errorf("placement changed")
	}
	if err = fsutil.WriteJSONAtomic(filepath.Join(backup, "placements.json"), original, 0600); err != nil {
		return out, err
	}
	team, err := readCrewTeamOrNew(project, person)
	if err != nil || team.OwnerID != person || team.RosterSource != "" {
		return out, fmt.Errorf("source Team changed")
	}
	if err = fsutil.WriteJSONAtomic(filepath.Join(backup, "team.json"), team, 0600); err != nil {
		return out, err
	}
	log.Printf("[AGENT_OFFLINE_COPY] project=%s tab=%s event=conversion_backup_saved", project, placement.Tab)
	// The old scoped trees remain inert in place. No content is copied to the home.
	scoped, err := crewAgentScopedRoots(roots, placement.Tab)
	if err != nil {
		return out, err
	}
	keptRoot := scoped.Resources
	if placement.SplitFiles {
		keptRoot = scoped.Files
	}
	if err = filepath.WalkDir(keptRoot, func(_ string, d os.DirEntry, e error) error {
		if os.IsNotExist(e) {
			return nil
		}
		if e != nil {
			return e
		}
		if d.Type().IsRegular() {
			out.FilesKeptAside++
		}
		return nil
	}); err != nil {
		return out, err
	}
	// An old relay registry may still label this exact placed tab as Mac.
	// Only the independently verified saved server conversation changes place.
	if row, ok := team.registeredAgent(person, record.ConversationKey); ok {
		if row.Terminal != nil {
			return out, fmt.Errorf("old source is terminal")
		}
		if err = writeCrewTeamWithAgents(team, func(rows map[string]crewTeamRegisteredAgent) {
			current, ok := rows[row.ID]
			if ok && current.OwnerID == person && current.ChatKey == record.ConversationKey {
				current.Place = "server"
				rows[row.ID] = current
			}
		}); err != nil {
			return out, err
		}
	}
	id, actual, err := registerCrewPutOnlineServerSource(ctx, person, project, record.ConversationKey)
	if err != nil || actual.SessionID != record.SessionID {
		return out, fmt.Errorf("owned server source unavailable")
	}
	out.Agent = id
	// This migration's explicit privacy reset is not a normal last-shared toggle.
	// The owner re-shares the converted source through the normal Team choices.
	team, err = readCrewTeam(project)
	if err != nil {
		return out, err
	}
	if err = writeCrewTeamWithAgents(team, func(rows map[string]crewTeamRegisteredAgent) {
		a, ok := rows[id]
		if ok && a.OwnerID == person && a.ChatKey == record.ConversationKey {
			a.InTeam = false
			rows[id] = a
		}
	}); err != nil {
		return out, err
	}
	if _, err = api.putCrewAgentOnline(ctx, person, project, crewPutOnlineRequest{Agent: id, Audience: "only_me"}); err != nil {
		return out, err
	}
	if err = api.syncCrewOfflineServerTurn(ctx, person, record.SessionID, record.WorkspacePath); err != nil {
		return out, err
	}
	api.refreshCrewOfflineCopy(project, id)
	team, err = readCrewTeam(project)
	if err != nil {
		return out, err
	}
	ref, ready, err := crewOfflineCopyReady(team, team.Agents[id])
	if err != nil || !ready {
		log.Printf("[AGENT_OFFLINE_COPY] project=%s agent=%s event=conversion_preparing audience=only_me", project, id)
		return out, err
	}
	if err = finishCrewOnlineConversion(ctx, person, project, placement, record, id, ref.Home); err != nil {
		return out, err
	}
	out.Ready = true
	log.Printf("[AGENT_OFFLINE_COPY] project=%s agent=%s copy=%s event=converted audience=only_me files_kept_aside=%d mounted=0", project, id, ref.ChatKey, out.FilesKeptAside)
	return out, nil
}

// Compare the full saved placement, including the selected-file declaration.
func sameCrewOnlinePlacement(a, b crewAgentPlacement) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

func finishCrewOnlineConversion(ctx context.Context, person, project string, placement crewAgentPlacement, record ProductConversationRecord, id, home string) error {
	teamDir, err := crewTeamDir(project)
	if err != nil {
		return err
	}
	mu := crewTeamRecordMutex(teamDir)
	mu.Lock()
	defer mu.Unlock()
	team, err := readCrewTeam(project)
	if err != nil {
		return err
	}
	a, ok := team.Agents[id]
	if !ok || a.ChatKey != record.ConversationKey || a.OwnerID != person || a.InTeam || crewOfflineSourceOwner(team, person, a) != nil {
		return fmt.Errorf("source changed")
	}
	if _, err = crewOnlinePlacementRecord(ctx, person, project, placement); err != nil {
		return err
	}
	ref, ready, err := crewOfflineCopyReady(team, a)
	if err != nil || !ready || ref.Home != home {
		return fmt.Errorf("copy changed")
	}
	dir, err := crewOfflineDirectory(project, id)
	if err != nil {
		return err
	}
	crewAgentPlacementMu.Lock()
	defer crewAgentPlacementMu.Unlock()
	placements, err := readCrewAgentPlacements(project)
	if err != nil {
		return err
	}
	current, ok := placements[placement.Tab]
	if !ok {
		return nil
	}
	if !sameCrewOnlinePlacement(current, placement) {
		return fmt.Errorf("placement changed")
	}
	// Raw-map update keeps the execution adapter's private fields, including new ones.
	raw, err := os.ReadFile(filepath.Join(dir, "copy.json"))
	if err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	fields["source_retired"] = json.RawMessage("true")
	if err = fsutil.WriteJSONAtomic(filepath.Join(dir, "copy.json"), fields, 0600); err != nil {
		return err
	}
	delete(placements, placement.Tab)
	path, err := crewAgentPlacementsPath(project)
	if err != nil {
		return err
	}
	return fsutil.WriteJSONAtomic(path, placements, 0600)
}

// The old tab remains an identity and history source, never a person-slot
// execution target after conversion. The UI opens its source-gated Ask instead.
func crewRetiredOnlineSource(project, key string) (string, error) {
	team, err := readCrewTeam(project)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	a, ok := team.registeredAgent(team.OwnerID, key)
	if !ok {
		return "", nil
	}
	dir, err := crewOfflineDirectory(project, a.ID)
	if err != nil {
		return "", err
	}
	raw, err := os.ReadFile(filepath.Join(dir, "copy.json"))
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var ref struct {
		SourceRetired bool `json:"source_retired"`
	}
	if err = json.Unmarshal(raw, &ref); err != nil {
		return "", err
	}
	if !ref.SourceRetired {
		return "", nil
	}
	return crewTeamAgentBridgeAddress(a.OwnerID, a.ID), nil
}
