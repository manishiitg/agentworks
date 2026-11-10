package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/mux"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

// The chunked sync protocol (crew_team_chunks.go). Every request is bounded (a commit carries at most
// crewTeamCommitBatch files, a chunk request or reply at most chunkBatchBytes bytes), so none runs into the 20 s the
// company gateway and the Mac allow; Team membership is checked on every request.

// crewTeamSyncRequest is a plan or a commit: the changed files (and, for a commit, the sender's transfer id).
type crewTeamSyncRequest struct {
	Client     string           `json:"client"`
	TransferID string           `json:"transfer_id,omitempty"`
	Changes    []crewTeamChange `json:"changes"`
	// OnPurpose are files the sender chose to sync despite credential-looking names (owner, 5 Nov, "Sync it anyway").
	OnPurpose []string `json:"on_purpose,omitempty"`
	// Since is the last revision this copy has, for changes and every upload.
	Since uint64 `json:"since,omitempty"`
}

// crewTeamRemoteFile is one file the copy should have: its current version, or nil when it was removed.
type crewTeamRemoteFile struct {
	Path    string       `json:"path"`
	Version *chunkedFile `json:"version,omitempty"`
}

// crewTeamChangesReply lists what changed after the copy's revision, up to Through (more pages while More).
type crewTeamChangesReply struct {
	CatchUp   bool                 `json:"catch_up,omitempty"`
	Revision  uint64               `json:"revision"`
	Through   uint64               `json:"through"`
	More      bool                 `json:"more,omitempty"`
	Files     []crewTeamRemoteFile `json:"files"`
	OnPurpose []string             `json:"on_purpose,omitempty"`
}

// crewTeamSyncBusy counts the sync requests a Team's server is serving now (a commit, chunks in or out), for the Crew
// panel's Busy; each start and end tells the panel to read again.
var crewTeamSyncBusy sync.Map // project -> *atomic.Int64

func crewTeamSyncWork(project string) func() {
	value, _ := crewTeamSyncBusy.LoadOrStore(project, new(atomic.Int64))
	counter := value.(*atomic.Int64)
	counter.Add(1)
	publishCrewPanelChanged(project)
	return func() {
		counter.Add(-1)
		publishCrewPanelChanged(project)
	}
}

func crewTeamSyncActive(project string) bool {
	value, ok := crewTeamSyncBusy.Load(project)
	return ok && value.(*atomic.Int64).Load() > 0
}

// crewTeamGuardAllows applies a scoped connection's file guard to one path.
func crewTeamGuardAllows(claims *UserClaims, rel string, write bool) bool {
	return claims.AccessToken == nil || claims.AccessToken.FileGuard == nil || claims.AccessToken.FileGuard.Allows(rel, write)
}

func (api *StreamingAPI) handleCrewTeamSync(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	project, op := mux.Vars(r)["project"], mux.Vars(r)["op"]
	write := op != "changes"
	if _, e := crewTeamRequestAuthorize(r, claims, project, ".", write, true); e != nil {
		externalError(w, 403, "team_folder_forbidden", e.Error())
		return
	}
	if op == "commit" {
		defer crewTeamSyncWork(project)()
	}
	start := time.Now()
	var in crewTeamSyncRequest
	if json.NewDecoder(io.LimitReader(r.Body, 32<<20)).Decode(&in) != nil || len(in.Changes) > crewTeamCommitBatch || !crewTeamClientID(in.Client) {
		externalError(w, 400, "invalid_transfer", fmt.Sprintf("Send at most %d changed files with this copy's id.", crewTeamCommitBatch))
		return
	}
	done := func(status int, files, chunks int) {
		log.Printf("[CREW_TEAM_SYNC] project=%s actor=%s action=%s files=%d chunks=%d status=%d ms=%d", project, logUsernameForUserID(claims.UserID), op, files, chunks, status, time.Since(start).Milliseconds())
	}
	switch op {
	case "plan":
		state, e := readCrewTeamCurrentState(project)
		if e != nil {
			externalError(w, 409, "transfer_failed", e.Error())
			return
		}
		if in.Since < state.HistoryFloor || in.Since > state.Revision {
			team, _ := readCrewTeam(project)
			writeCrewTeamCatchUp(w, project, claims, team, state, in.Since)
			return
		}
		team, _ := readCrewTeam(project)
		store, e := crewTeamChunkStore(project)
		if e != nil {
			externalError(w, 409, "transfer_failed", e.Error())
			return
		}
		onPurpose := state.onPurpose().with(in.OnPurpose)
		afters := []chunkedFile{}
		for _, c := range in.Changes {
			if !crewTeamAllowed(c.Path, onPurpose) || !crewTeamGuardAllows(claims, c.Path, true) || c.After != nil && (!team.Folder.holds(c.Path) || !c.After.Valid()) {
				done(409, len(in.Changes), 0)
				externalError(w, 409, "transfer_failed", c.Path+" is not a file this team shares")
				return
			}
			if c.After != nil {
				afters = append(afters, *c.After)
			}
		}
		missing := missingChunks(store, afters)
		done(200, len(in.Changes), len(missing))
		externalJSON(w, map[string]any{"missing": missing})
	case "commit":
		for _, c := range in.Changes {
			if !crewTeamGuardAllows(claims, c.Path, true) {
				externalError(w, 403, "team_folder_forbidden", "This connection cannot change that shared file.")
				return
			}
		}
		lockDir, _ := crewTeamDir(project)
		unlock, e := wf.LockWorkspace(r.Context(), lockDir)
		if e != nil {
			externalError(w, 409, "transfer_failed", e.Error())
			return
		}
		defer unlock()
		if _, e = crewTeamRequestAuthorize(r, claims, project, ".", true, true); e != nil {
			externalError(w, 403, "team_folder_forbidden", e.Error())
			return
		}
		wire, _ := json.Marshal(in.Changes)
		receipt, e := commitCrewTeamChangesAtRevision(r.Context(), project, claims.UserID, in.Client+"/"+in.TransferID, crewTransferDigest(string(wire)), &in.Since, in.Changes, in.OnPurpose...)
		if errors.Is(e, errCrewTeamHistoryCatchUp) {
			state, readErr := loadCrewTeamFileState(project, true)
			if readErr != nil {
				externalError(w, 409, "transfer_failed", readErr.Error())
				return
			}
			team, _ := readCrewTeam(project)
			writeCrewTeamCatchUp(w, project, claims, team, state, in.Since)
			return
		}
		if e != nil {
			done(409, len(in.Changes), 0)
			externalError(w, 409, "transfer_failed", e.Error())
			return
		}
		done(200, len(in.Changes), 0)
		externalJSON(w, receipt)
	case "changes":
		state, paths, through, e := readCrewTeamLedgerChanges(project, in.Since, crewTeamCommitBatch)
		if e != nil {
			externalError(w, 409, "transfer_failed", e.Error())
			return
		}
		if in.Since < state.HistoryFloor || in.Since > state.Revision {
			state, e = readCrewTeamCurrentState(project)
			if e != nil {
				externalError(w, 409, "transfer_failed", e.Error())
				return
			}
			team, _ := readCrewTeam(project)
			out := crewTeamCatchUpManifest(claims, team, state)
			log.Printf("[CREW_TEAM_LEDGER] project=%s event=sync_catch_up requested_revision=%d current_revision=%d history_floor=%d", project, in.Since, state.Revision, state.HistoryFloor)
			done(200, len(out.Files), 0)
			externalJSON(w, out)
			return
		}
		out := crewTeamChangesReply{Revision: state.Revision, Through: state.Revision, Files: []crewTeamRemoteFile{}, OnPurpose: sortedOnPurpose(state.onPurpose())}
		if through < state.Revision {
			out.Through, out.More = through, true
		}
		op := state.onPurpose()
		team, _ := readCrewTeam(project)
		for _, p := range paths {
			if !crewTeamAllowed(p, op) || !team.Folder.holds(p) || !crewTeamGuardAllows(claims, p, false) {
				continue
			}
			file := crewTeamRemoteFile{Path: p}
			if v := state.Versions[p]; v.File != nil {
				file.Version = &chunkedFile{Hash: v.File.Hash, Size: v.File.Size, Exec: v.File.Executable, Chunks: v.Chunks}
			}
			out.Files = append(out.Files, file)
		}
		done(200, len(out.Files), 0)
		externalJSON(w, out)
	}
}

// crewTeamChangedSince lists the paths changed after since, a whole revision at a time, stopping once limit paths are
// listed; through is the last revision included.
func crewTeamChangedSince(state crewTeamFileState, since uint64, limit int) ([]string, uint64) {
	byRevision := map[uint64][]string{}
	revisions := []uint64{}
	for _, e := range state.Events {
		if e.Revision <= since {
			continue
		}
		if _, ok := byRevision[e.Revision]; !ok {
			revisions = append(revisions, e.Revision)
		}
		byRevision[e.Revision] = append(byRevision[e.Revision], e.Path)
	}
	sort.Slice(revisions, func(i, j int) bool { return revisions[i] < revisions[j] })
	seen, paths, through := map[string]bool{}, []string{}, state.Revision
	for i, rev := range revisions {
		if len(paths) >= limit && i > 0 {
			through = revisions[i-1]
			break
		}
		for _, p := range byRevision[rev] {
			if !seen[p] {
				seen[p] = true
				paths = append(paths, p)
			}
		}
	}
	sort.Strings(paths)
	return paths, through
}

// handleCrewTeamChunks receives chunks (PUT: frames, verified and stored once) or sends the chunks asked for (POST
// {ids}: frames, at most chunkBatchBytes bytes per reply; the caller asks again for the rest).
func (api *StreamingAPI) handleCrewTeamChunks(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	project := mux.Vars(r)["project"]
	write := r.Method == http.MethodPut
	if _, e := crewTeamRequestAuthorize(r, claims, project, ".", write, true); e != nil {
		externalError(w, 403, "team_folder_forbidden", e.Error())
		return
	}
	store, e := crewTeamChunkStore(project)
	if e != nil {
		externalError(w, 409, "transfer_failed", e.Error())
		return
	}
	defer crewTeamSyncWork(project)()
	start := time.Now()
	if write {
		stored, size, e := receiveChunks(r.Body, store)
		log.Printf("[CREW_TEAM_SYNC] project=%s actor=%s action=send chunks=%d bytes=%d ms=%d err=%v", project, logUsernameForUserID(claims.UserID), stored, size, time.Since(start).Milliseconds(), e)
		if e != nil {
			externalError(w, 400, "transfer_failed", e.Error())
			return
		}
		externalJSON(w, map[string]int{"stored": stored})
		return
	}
	// Only chunks of a file the caller may read now: a current version, inside what the team shares, not private, and
	// within their connection's file guard (not any old chunk by its hash).
	sent, size, ok := serveChunks(w, r, store, func() (map[string]bool, error) {
		state, e := readCrewTeamCurrentState(project)
		if e != nil {
			return nil, e
		}
		team, _ := readCrewTeam(project)
		readable, op := map[string]bool{}, state.onPurpose()
		for p, v := range state.Versions {
			if v.File == nil || !crewTeamAllowed(p, op) || !team.Folder.holds(p) || !crewTeamGuardAllows(claims, p, false) {
				continue
			}
			for _, c := range v.Chunks {
				readable[c.ID] = true
			}
		}
		return readable, nil
	})
	if ok {
		log.Printf("[CREW_TEAM_SYNC] project=%s actor=%s action=receive chunks=%d bytes=%d ms=%d", project, logUsernameForUserID(claims.UserID), sent, size, time.Since(start).Milliseconds())
	}
}

func (api *StreamingAPI) registerCrewTeamRoutes(r *mux.Router) {
	r.HandleFunc("/crew-teams", api.handleCrewTeam).Methods("GET")
	r.HandleFunc("/crew-teams/{project}", api.handleCrewTeam).Methods("GET", "PUT")
	r.HandleFunc("/crew-teams/{project}/{op:invite}", api.handleCrewTeam).Methods("POST")
	r.HandleFunc("/crew-teams/{project}/agents", api.handleCrewTeamAgents).Methods("GET")
	r.HandleFunc("/crew-teams/{project}/activity", api.handleCrewTeamActivity).Methods("GET")
	r.HandleFunc("/crew-teams/{project}/tab", api.handleCrewTeamTab).Methods("GET")
	r.HandleFunc("/crew-teams/{project}/dashboards-tab", api.handleCrewTeamDashboardsTab).Methods("GET")
	r.HandleFunc("/crew-teams/{project}/share", api.handleCrewSharePage).Methods("GET")
	r.HandleFunc("/crew-teams/{project}/action", api.handleCrewShareAction).Methods("POST")
	r.HandleFunc("/crew-teams/{project}/dashboards/public-link", api.handleCrewTeamDashboardPublicLink).Methods("POST")
	r.HandleFunc("/crew-teams/{project}/agentbridge", api.handleCrewTeamAgentBridge).Methods("POST")
	r.HandleFunc("/crew-teams/{project}/folders", api.handleCrewTeamFolders).Methods("GET")
	r.HandleFunc("/crew-teams/{project}/folder-change", api.handleCrewTeamFolderChange).Methods("GET")
	r.HandleFunc("/crew-teams/{project}/where", api.handleCrewTeamWhere).Methods("POST")
	r.HandleFunc("/crew-teams/{project}/setup", api.handleCrewTeamSetup).Methods("GET", "POST")
	r.HandleFunc("/crew-teams/{project}/people", api.handleCrewTeamPeople).Methods("GET", "POST")
	r.HandleFunc("/crew-teams/{project}/{op:leave|agent-visibility|agent-rule}", api.handleCrewTeamMemberAction).Methods("POST")
	r.HandleFunc("/crew-teams/{project}/{op:files}", api.handleCrewTeamFiles).Methods("GET")
	r.HandleFunc("/crew-teams/{project}/{op:file}", api.handleCrewTeamFiles).Methods("GET", "PUT", "DELETE")
	r.HandleFunc("/crew-teams/{project}/{op:events|clashes|history|compare}", api.handleCrewTeamHistory).Methods("GET")
	r.HandleFunc("/crew-teams/{project}/{op:resolve}", api.handleCrewTeamHistory).Methods("POST")
	r.HandleFunc("/crew-teams/{project}/sync/{op:plan|commit|changes}", api.handleCrewTeamSync).Methods("POST")
	r.HandleFunc("/crew-teams/{project}/chunks", api.handleCrewTeamChunks).Methods("PUT", "POST")
	r.HandleFunc("/desktop-connections/{connection}/crew-teams/{project}/local", api.handleDesktopTeamStatus).Methods("GET")
	r.HandleFunc("/desktop-connections/{connection}/crew-teams/{project}/sync-anyway", api.handleDesktopTeamSyncAnyway).Methods("POST")
	r.HandleFunc("/desktop-connections/{connection}/crew-teams/{project}", api.handleDesktopCrewTeam).Methods("GET", "PUT")
	r.HandleFunc("/desktop-connections/{connection}/crew-teams/{project}/switch", api.handleDesktopCrewTeamSwitch).Methods("POST")
	r.HandleFunc("/desktop-connections/{connection}/crew-teams/{project}/join", api.handleDesktopCrewTeamJoin).Methods("GET", "POST")
	r.HandleFunc("/desktop-connections/{connection}/crew-teams/{project}/remove-from-mac", api.handleDesktopCrewTeamRemoveFromMac).Methods("POST")
	r.HandleFunc("/desktop-connections/{connection}/crew-teams/{project}/add-to-mac", api.handleDesktopCrewTeamAddToMac).Methods("POST")
}

// Keep request identifiers usable in private state without accepting a path.
func crewTeamClientID(value string) bool {
	return value != "" && len(value) <= 128 && !strings.ContainsAny(value, "/\\\x00")
}

func sortedOnPurpose(op crewTeamOnPurpose) []string {
	out := make([]string, 0, len(op))
	for p := range op {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}
