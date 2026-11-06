package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/workspace/chunks"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/coding-agent-loop/workspace/crewsync"
	"github.com/manishiitg/coding-agent-loop/workspace/publication"
)

// A Crew that lives on a Mac reaches its Team's dashboards without moving online (owner, 6 Nov, decisions log). The Mac
// sends db/reports, db/assets and a consistent copy of the Crew's database as chunks; the server keeps them in the Crew's
// own read copy, crewroot.MacDashboardsFolder, swapped whole. The folder is apart from the Crew's db/ on purpose: an
// always-on agent may write there while the Mac is off, and with the Team on Whole Crew, Team sync writes db/reports.

const (
	crewMacDashboardsDatabase = "db/db.sqlite"
	crewMacDashboardsEvery    = 5 * time.Minute
)

// crewMacDashboardsPath: a file a Mac sends. Everything else stays on the Mac.
func crewMacDashboardsPath(rel string) bool {
	if _, err := publication.CleanPath(rel); err != nil {
		return false
	}
	if rel == crewMacDashboardsDatabase {
		return true
	}
	return (strings.HasPrefix(rel, "db/reports/") || strings.HasPrefix(rel, "db/assets/")) && !publication.ExcludedFromSync(rel, false) && !publication.CredentialPath(rel)
}

// validateCrewMacDashboards is the one check of what a Mac names, before any byte is assembled.
func validateCrewMacDashboards(files map[string]chunks.File) error {
	if len(files) == 0 || len(files) > crewsync.SyncMaxFiles {
		return fmt.Errorf("send the dashboard files")
	}
	var total int64
	for rel, v := range files {
		if !crewMacDashboardsPath(rel) || !v.Valid() || rel != crewMacDashboardsDatabase && v.Size > publication.MaxFileBytes {
			return fmt.Errorf("invalid dashboard file")
		}
		total += v.Size
	}
	if total > crewsync.SyncMaxBytes {
		return fmt.Errorf("this Crew's dashboards are over the %d GB limit", crewsync.SyncMaxBytes>>30)
	}
	return nil
}

type crewMacDashboardsRequest struct {
	Files map[string]chunks.File `json:"files"`
}

func crewMacDashboardsStore(id string) (string, error) {
	p, err := crewSyncManifestPath(id)
	if err != nil {
		return "", err
	}
	dir := p + ".dashboard-chunks"
	return dir, agentworksclient.EnsurePrivateDirectory(dir)
}

// crewMacDashboardsUpdated is when the read copy was last swapped, from its own record ("" none yet).
func crewMacDashboardsUpdated(id string) (time.Time, bool) {
	p, err := crewSyncManifestPath(id)
	if err != nil {
		return time.Time{}, false
	}
	info, err := os.Stat(p + ".dashboards-updated")
	if err != nil {
		return time.Time{}, false
	}
	return info.ModTime(), true
}

// handleCrewMacDashboards is /api/crew-sync/{project}/dashboards/{plan|chunks|commit}. Every request rechecks the actual
// owner, the pair and that the Crew still lives on the Mac.
func (api *StreamingAPI) handleCrewMacDashboards(w http.ResponseWriter, r *http.Request) {
	id, op := mux.Vars(r)["project"], mux.Vars(r)["op"]
	pair := r.URL.Query().Get("pair")
	started := time.Now()
	fail := func(e error) {
		status := 409
		if f, ok := e.(*crewSyncFailure); ok && f.Status != 0 {
			status = f.Status
		}
		log.Printf("[CREW_MAC_DASHBOARDS] project=%s op=%s status=%d error=%q", id, op, status, e)
		externalError(w, status, crewSyncErrorCode(e), e.Error())
	}
	roots, b, e := api.crewTransferBinding(r.Context(), GetUserFromContext(r.Context()), id, pair)
	if e != nil {
		fail(e)
		return
	}
	if b.Editing != "local" {
		fail(fmt.Errorf("this Crew lives on the server"))
		return
	}
	store, e := crewMacDashboardsStore(id)
	if e != nil {
		fail(e)
		return
	}
	if op == "chunks" {
		stored, size, err := receiveChunks(r.Body, store)
		if err != nil {
			fail(err)
			return
		}
		log.Printf("[CREW_MAC_DASHBOARDS] project=%s op=send chunks=%d bytes=%d", id, stored, size)
		externalJSON(w, map[string]int{"stored": stored})
		return
	}
	var in crewMacDashboardsRequest
	if json.NewDecoder(io.LimitReader(r.Body, 16<<20)).Decode(&in) != nil {
		fail(fmt.Errorf("send the dashboard files"))
		return
	}
	if e = validateCrewMacDashboards(in.Files); e != nil {
		fail(e)
		return
	}
	files := make([]chunks.File, 0, len(in.Files))
	for _, v := range in.Files {
		files = append(files, v)
	}
	if op == "plan" {
		externalJSON(w, map[string]any{"missing": missingChunks(store, files)})
		return
	}
	if e = swapCrewMacDashboards(roots.Files, store, in.Files); e != nil {
		fail(e)
		return
	}
	if p, err := crewSyncManifestPath(id); err == nil {
		_ = fsutil.WriteJSONAtomic(p+".dashboards-updated", map[string]int{"files": len(in.Files)}, 0600)
	}
	used := map[string]bool{}
	for _, v := range files {
		for _, c := range v.Chunks {
			used[c.ID] = true
		}
	}
	pruneChunks(store, used, crewNativeChunkKeep)
	log.Printf("[CREW_MAC_DASHBOARDS] project=%s op=commit files=%d ms=%d", id, len(in.Files), time.Since(started).Milliseconds())
	externalJSON(w, map[string]bool{"complete": true})
}

// swapCrewMacDashboards builds the new read copy beside the old one and swaps it in by rename, so a reader sees one or
// the other, never a mix.
func swapCrewMacDashboards(root, store string, files map[string]chunks.File) error {
	target := filepath.Join(root, crewroot.MacDashboardsFolder)
	next := target + ".next-" + uuid.NewString()
	defer os.RemoveAll(next)
	for rel := range files {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(next, filepath.FromSlash(rel))), 0o770); err != nil {
			return err
		}
	}
	err := assembleFiles(files, func(rel string) (string, error) { return filepath.Join(next, filepath.FromSlash(rel)), nil }, func(c transferChunk) ([]byte, error) { return readChunk(store, c) })
	if err != nil {
		return err
	}
	old := target + ".old-" + uuid.NewString()
	if err = os.Rename(target, old); err != nil && !os.IsNotExist(err) {
		return err
	}
	defer os.RemoveAll(old)
	return os.Rename(next, target)
}

// crewMacDashboardsRead is where a read of a Crew's dashboard file or database goes: to the read copy for a Crew on a
// Mac, once it has one; raw otherwise. Writes never come here.
func crewMacDashboardsRead(ctx context.Context, raw string) string {
	ref, ok := resolveCrewPath(ctx, GetUserIDFromContext(ctx), raw)
	if !ok || ref.Rest != crewMacDashboardsDatabase && !strings.HasPrefix(ref.Rest, "db/reports/") && !strings.HasPrefix(ref.Rest, "db/assets/") {
		return raw
	}
	if root := crewMacDashboardsRoot(ref.Root); root != ref.Root {
		return root + "/" + ref.Rest
	}
	return raw
}

// crewMacDashboardsRoot is the root dashboards of the Crew at root are read from.
func crewMacDashboardsRoot(root string) string {
	rec, found := crewOwnerRecordAt(root)
	if !found || !crewRelayTwin(rec.ProjectID) {
		return root
	}
	if _, ok := crewMacDashboardsUpdated(rec.ProjectID); !ok {
		return root
	}
	return root + "/" + crewroot.MacDashboardsFolder
}

// The Mac's side.

var crewMacDashboardsSent sync.Map // local project -> time of the last send

// crewMacDashboardsDue: something under db/reports, db/assets or the database changed since the last send, at most one
// send every crewMacDashboardsEvery.
func crewMacDashboardsDue(id string, sources ...string) bool {
	last, _ := crewMacDashboardsSent.Load(id)
	sent, _ := last.(time.Time)
	if time.Since(sent) < crewMacDashboardsEvery {
		return false
	}
	changed := false
	for _, source := range sources {
		_ = filepath.WalkDir(source, func(_ string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if info, e := d.Info(); e == nil && info.ModTime().After(sent) {
				changed = true
				return filepath.SkipAll
			}
			return nil
		})
	}
	return changed
}

// sendCrewMacDashboards sends this Mac Crew's dashboards to its server copy when they changed: every file named,
// only the chunks the server lacks moved.
func (api *StreamingAPI) sendCrewMacDashboards(ctx context.Context, owner string, c desktopConnection, local string) error {
	binding, err := crewroot.ReadSync(local)
	if err != nil || binding == nil || binding.Side != "local" || binding.Editing != "local" {
		return err
	}
	roots, found, err := crewStorageRoots(crewProjectRootByID(local))
	if err != nil || !found {
		return err
	}
	reports, assets := filepath.Join(roots.Resources, "db", "reports"), filepath.Join(roots.Resources, "db", "assets")
	database, _ := crewMoveRecordPath(roots, crewMacDashboardsDatabase)
	if !crewMacDashboardsDue(local, reports, assets, database) {
		return nil
	}
	started := time.Now()
	stage, err := os.MkdirTemp("", "agentworks-dashboards-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	names := []string{}
	sourceOf := map[string]string{}
	for _, folder := range []string{"db/reports", "db/assets"} {
		base := filepath.Join(roots.Resources, filepath.FromSlash(folder))
		_ = filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
			if err != nil || !d.Type().IsRegular() {
				return nil
			}
			rel, _ := filepath.Rel(roots.Resources, p)
			if rel = filepath.ToSlash(rel); crewMacDashboardsPath(rel) {
				names, sourceOf[rel] = append(names, rel), p
			}
			return nil
		})
	}
	if len(names) == 0 {
		return nil
	}
	if database != "" && fileExists(database) {
		// The live database is never read directly: a consistent copy, as a move makes.
		copied := filepath.Join(stage, "db.sqlite")
		if err = snapshotCrewMoveDatabase(ctx, database, copied); err != nil {
			return err
		}
		names, sourceOf[crewMacDashboardsDatabase] = append(names, crewMacDashboardsDatabase), copied
	}
	slices.Sort(names)
	files, sources, err := chunkFiles(names, func(rel string) (string, error) { return sourceOf[rel], nil }, true, nil)
	if err != nil {
		return err
	}
	// Chunks are read back from where they were cut, so sources hold full paths under "/".
	for id, s := range sources {
		s.path = sourceOf[s.path]
		sources[id] = s
	}
	route := "/api/crew-sync/" + url.PathEscape(binding.PeerProjectID) + "/dashboards"
	query := "?" + url.Values{"pair": {binding.PairID}}.Encode()
	request := crewMacDashboardsRequest{Files: files}
	var plan struct {
		Missing []string `json:"missing"`
	}
	if err = crewTeamRemoteJSON(ctx, owner, c, http.MethodPost, route+"/plan"+query, request, &plan); err != nil {
		return err
	}
	sent, size, err := sendMissingChunks(ctx, owner, c, route+"/chunks"+query, "/", sources, plan.Missing, nil)
	if err != nil {
		return err
	}
	if err = crewTeamRemoteJSON(ctx, owner, c, http.MethodPost, route+"/commit"+query, request, nil); err != nil {
		return err
	}
	crewMacDashboardsSent.Store(local, time.Now())
	all := make([]chunks.File, 0, len(files))
	for _, v := range files {
		all = append(all, v)
	}
	logChunkTransfer("dashboards", local, "up", len(all), distinctChunks(all), sent, size, started)
	return nil
}
