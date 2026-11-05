package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/coding-agent-loop/workspace/crewsync"
	"github.com/manishiitg/coding-agent-loop/workspace/publication"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

type crewTeamLocal struct {
	FolderID     string `json:"folder_id"`
	Connection   string `json:"connection"`
	Project      string `json:"project_id"`
	LocalProject string `json:"local_project_id,omitempty"`
	RemoteUser   string `json:"remote_user"`
	Client       string `json:"client"`
	Root         string `json:"local_folder"`
	// Files is what this copy last had in step with the team, per path: size and modification time (a file whose
	// size and time are unchanged is not read again) and its version. Revision is the team revision it has received.
	Files      map[string]crewTeamLocalFile `json:"files"`
	Revision   uint64                       `json:"revision"`
	LastSynced string                       `json:"last_synced,omitempty"`
	Status     string                       `json:"status"`
	Error      string                       `json:"error,omitempty"`
	// "Sync it anyway" (owner, 5 Nov): this person's own choice of files that look like they hold passwords or keys,
	// by exact path; StopSyncing are ones taken back, removed from the shared folder on the next upload (the files
	// stay here). FolderOnPurpose is what the shared folder holds on purpose, from the server's last answer.
	SyncAnyway      []string `json:"sync_anyway,omitempty"`
	StopSyncing     []string `json:"stop_syncing,omitempty"`
	FolderOnPurpose []string `json:"folder_on_purpose,omitempty"`
}

type crewTeamLocalFile struct {
	Size    int64               `json:"size"`
	Mtime   int64               `json:"mtime"`
	Version crewTeamFileVersion `json:"version"`
}

// onPurpose is what this copy syncs on purpose: its own choices and the folder's, minus the ones it took back.
func (b crewTeamLocal) onPurpose() crewTeamOnPurpose {
	op := crewTeamOnPurpose{}.with(b.SyncAnyway).with(b.FolderOnPurpose)
	for _, p := range b.StopSyncing {
		delete(op, p)
	}
	return op
}

func crewTeamLocalDir(owner string, c desktopConnection, project, folderID string) (string, error) {
	root, e := crewroot.StateRoot()
	if e != nil {
		return "", e
	}
	return filepath.Join(root, "team-working", crewTransferDigest(owner), crewTransferDigest(c.Server+"\x00"+c.User.ID+"\x00"+project+"\x00"+folderID+"\x00"+crewTeamSyncFormat)), nil
}
func readCrewTeamLocal(dir string) (crewTeamLocal, error) {
	var b crewTeamLocal
	raw, e := os.ReadFile(filepath.Join(dir, "binding.json"))
	if e == nil {
		e = json.Unmarshal(raw, &b)
	}
	return b, e
}
func saveCrewTeamLocal(dir string, b crewTeamLocal) error {
	err := fsutil.WriteJSONAtomic(filepath.Join(dir, "binding.json"), b, 0600)
	if err == nil {
		publishCrewPanelChanged(b.LocalProject)
	}
	return err
}

// The server remains the authority. A local binding stores only a working path,
// transfer progress and two baselines; it never grants Team membership.
func (api *StreamingAPI) ensureCrewTeamLocal(ctx context.Context, owner string, c desktopConnection, t crewTeam) (string, crewTeamLocal, error) {
	dir, e := crewTeamLocalDir(owner, c, t.ProjectID, t.Folder.ID)
	if e != nil {
		return "", crewTeamLocal{}, e
	}
	if b, e := readCrewTeamLocal(dir); e == nil {
		return dir, b, nil
	} else if !os.IsNotExist(e) {
		return dir, b, e
	}
	if !t.FolderAccess {
		return dir, crewTeamLocal{}, fmt.Errorf("ask the owner to allow shared folder editing")
	}
	b := crewTeamLocal{FolderID: t.Folder.ID, Connection: c.ID, Project: t.ProjectID, RemoteUser: c.User.ID, Client: uuid.NewString(), Status: "pending"}
	// The owner's selected folder stays where it is. A recipient receives a
	// fresh app-managed folder, never a merge into an unrelated local project.
	if c.User.ID == t.OwnerID {
		records, err := defaultProjectOwners().All()
		if err != nil {
			return dir, b, err
		}
		for _, rec := range records {
			if rec.OwnerID != owner {
				continue
			}
			pair, err := crewroot.ReadSync(rec.ProjectID)
			if err != nil || pair == nil || pair.Side != "local" || pair.ConnectionID != c.ID || pair.PeerProjectID != t.ProjectID {
				continue
			}
			roots, err := api.crewSyncRoots(ctx, &UserClaims{UserID: owner, Username: owner}, rec.ProjectID)
			if err != nil {
				return dir, b, err
			}
			// The Team syncs relative to the Crew's own folder (Whole Crew or chosen folders inside it).
			b.Root = roots.Files
			b.LocalProject = rec.ProjectID
			break
		}
	}
	if b.Root == "" {
		b.Root = filepath.Join(dir, "files")
	}
	if e = os.MkdirAll(b.Root, 0700); e != nil {
		return dir, b, e
	}
	if e = os.MkdirAll(dir, 0700); e != nil {
		return dir, b, e
	}
	return dir, b, saveCrewTeamLocal(dir, b)
}

func (api *StreamingAPI) startCrewTeamSync() context.CancelFunc {
	ctx, cancel := context.WithCancel(context.Background())
	if !IsLocalMode() {
		api.startCrewTeamServerCopies(ctx)
		return cancel
	}
	go func() {
		for ctx.Err() == nil {
			owner := GetDefaultUserID()
			connections, e := readDesktopConnections(owner)
			if e == nil {
				for _, c := range connections {
					check, stop := context.WithTimeout(ctx, 15*time.Second)
					var out struct {
						Teams []crewTeam `json:"teams"`
					}
					e = crewTeamRemoteJSON(check, owner, c, http.MethodGet, "/api/crew-teams", nil, &out)
					stop()
					if e != nil {
						continue
					}
					for _, t := range out.Teams {
						if _, joining := crewTeamJoining.Load(crewTeamJoinKey(c.ID, t.ProjectID)); !t.FolderAccess || joining {
							continue
						}
						dir, b, err := api.ensureCrewTeamLocal(ctx, owner, c, t)
						if err != nil {
							log.Printf("[CREW_TEAM_LOCAL] project=%s event=binding_failed reason=%q", t.ProjectID, err)
							continue
						}
						round, done := context.WithTimeout(ctx, 20*time.Minute) // a large first sync runs as server jobs the Mac polls
						local := crewTeamLocalCrew(b)
						api.reconcileCrewTeamSetup(round, owner, local, b.Root, dir, true) // this Crew's setup changes go up
						err = syncCrewTeamLocal(round, owner, c, dir, &b)
						if err == nil {
							api.reconcileCrewTeamSetup(round, owner, local, b.Root, dir, true) // someone's setup edit came down
						}
						done()
						if err != nil {
							b.Status = "pending"
							b.Error = crewTeamPlainSyncError(err)
							_ = saveCrewTeamLocal(dir, b)
							log.Printf("[CREW_TEAM] project=%s event=sync_failed step=%s shown=%q reason=%q", t.ProjectID, crewTeamStepOf(err), b.Error, err)
						}
					}
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
		}
	}()
	return cancel
}

func syncCrewTeamLocal(ctx context.Context, owner string, c desktopConnection, dir string, b *crewTeamLocal) error {
	unlock, e := wf.LockWorkspace(ctx, dir)
	if e != nil {
		return e
	}
	defer unlock()
	// The person's on-purpose choices may have changed since the caller read this copy (setCrewTeamSyncAnyway).
	if fresh, err := readCrewTeamLocal(dir); err == nil {
		b.SyncAnyway, b.StopSyncing = fresh.SyncAnyway, fresh.StopSyncing
	}
	if b.Files == nil {
		b.Files = map[string]crewTeamLocalFile{}
	}
	info, e := os.Lstat(b.Root)
	if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("the shared folder moved; open it in Teams to reconnect it")
	}
	if c.User.ID != b.RemoteUser {
		return fmt.Errorf("this folder belongs to a different saved account")
	}
	// Recheck before reading anything, in addition to the server's checks on every request.
	var team crewTeam
	if e = crewTeamRemoteJSON(ctx, owner, c, http.MethodGet, "/api/crew-teams/"+url.PathEscape(b.Project), nil, &team); e != nil {
		return e
	}
	if team.Folder.ID != b.FolderID {
		return fmt.Errorf("the shared folder changed; this previous copy is no longer shared")
	}
	if !team.FolderAccess {
		return fmt.Errorf("shared folder access was removed; downloaded files stay on this computer")
	}
	route := "/api/crew-teams/" + url.PathEscape(b.Project)
	query := "?folder_id=" + url.QueryEscape(b.FolderID)
	// 1. What changed here goes up: plan, the chunks the server lacks, commit; at most crewTeamCommitBatch files a time.
	scan, e := crewTeamScan(ctx, b, team.Folder)
	if e != nil {
		return crewTeamStep("scan", e)
	}
	if len(scan.changes) > 0 {
		var total int64
		for _, ch := range scan.changes {
			if ch.After != nil {
				total += ch.After.Size
			}
		}
		roundCtx := startCrewTeamRound(ctx, dir, b.Project, "upload", len(scan.changes), total, 0)
		e = crewTeamUpload(roundCtx, owner, c, route, query, b, scan)
		endCrewTeamRound(dir, e)
		if e != nil {
			return e
		}
		if e = saveCrewTeamLocal(dir, *b); e != nil {
			return e
		}
	}
	// 2. What changed on the team comes down, page by page.
	pending := false
	for {
		var page crewTeamChangesReply
		if e = crewTeamRemoteJSON(ctx, owner, c, http.MethodPost, route+"/sync/changes"+query, crewTeamSyncRequest{Client: b.Client, Since: b.Revision}, &page); e != nil {
			return crewTeamStep("changes", e)
		}
		b.FolderOnPurpose = page.OnPurpose // the server names what the folder holds on purpose
		skipped, err := crewTeamDownload(ctx, owner, c, route, query, dir, b, page.Files)
		if err != nil {
			return err
		}
		pending = pending || skipped
		b.Revision = page.Through
		if e = saveCrewTeamLocal(dir, *b); e != nil {
			return e
		}
		if !page.More {
			break
		}
	}
	b.Status = "up_to_date"
	if pending {
		b.Status = "pending"
	}
	b.Error = ""
	b.LastSynced = time.Now().UTC().Format(time.RFC3339Nano)
	log.Printf("[CREW_TEAM_LOCAL] project=%s client=%s event=reconciled status=%s files=%d revision=%d", b.Project, b.Client, b.Status, len(b.Files), b.Revision)
	return saveCrewTeamLocal(dir, *b)
}

// crewTeamChunkSource is where a chunk's bytes are in this copy: a file and the range inside it.
type crewTeamChunkSource struct {
	path   string
	offset int64
	size   int64
}

type crewTeamScanResult struct {
	changes []crewTeamChange
	entries map[string]crewTeamLocalFile // the new state of each changed path (absent for a removal)
	sources map[string]crewTeamChunkSource
}

// crewTeamScan finds what changed in this copy since it was last in step: a file whose size and modification time
// are unchanged is not read; any other is chunked. Only what the share holds is looked at (crewTeamAllowed, Sync it
// anyway, Whole Crew or the chosen folders, .gitignore), as the old capture did.
func crewTeamScan(ctx context.Context, b *crewTeamLocal, share crewTeamFolder) (crewTeamScanResult, error) {
	out := crewTeamScanResult{entries: map[string]crewTeamLocalFile{}, sources: map[string]crewTeamChunkSource{}}
	ignored, err := publication.GitIgnored(ctx, b.Root)
	if err != nil {
		return out, err
	}
	op := b.onPurpose()
	seen := map[string]bool{}
	var count, bytes int64
	err = filepath.WalkDir(b.Root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if p == b.Root {
			return nil
		}
		rel, _ := filepath.Rel(b.Root, p)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if !crewsync.Allowed(crewTeamKey(rel), true) || ignored[rel] || !share.walks(rel) {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 || !d.Type().IsRegular() || !crewsync.Allowed(crewTeamKey(rel), false) || ignored[rel] || !share.holds(rel) || !crewTeamAllowed(rel, op) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		seen[rel] = true
		count, bytes = count+1, bytes+info.Size()
		if count > publication.MaxFiles || bytes > publication.MaxBytes {
			return fmt.Errorf("shared folder exceeds 256 MiB or 10,000 files")
		}
		known, had := b.Files[rel]
		if had && known.Size == info.Size() && known.Mtime == info.ModTime().UnixNano() {
			return nil
		}
		f, err := openCrewTeamFile(b.Root, rel, op)
		if err != nil {
			return nil // a link or a file that went away: not shared this round
		}
		defer f.Close()
		var offset int64
		version, err := crewTeamChunkReader(f, info.Size(), info.Mode()&0o111 != 0, func(id string, data []byte) error {
			if _, ok := out.sources[id]; !ok {
				out.sources[id] = crewTeamChunkSource{path: rel, offset: offset, size: int64(len(data))}
			}
			offset += int64(len(data))
			return nil
		})
		if err != nil {
			return nil // changed while it was read: it goes next round
		}
		if had && known.Version.Hash == version.Hash {
			out.entries[rel] = crewTeamLocalFile{Size: info.Size(), Mtime: info.ModTime().UnixNano(), Version: version}
			return nil // touched, not changed: only its time is new
		}
		out.changes = append(out.changes, crewTeamChange{Path: rel, Before: known.Version.Hash, After: &version})
		out.entries[rel] = crewTeamLocalFile{Size: info.Size(), Mtime: info.ModTime().UnixNano(), Version: version}
		return nil
	})
	if err != nil {
		return out, err
	}
	// Removed here, or taken back from Sync it anyway (the file stays here): removed from the team.
	for rel, known := range b.Files {
		if !seen[rel] || slices.Contains(b.StopSyncing, rel) {
			out.changes = append(out.changes, crewTeamChange{Path: rel, Before: known.Version.Hash})
			delete(out.entries, rel)
		}
	}
	// Only touched files: record their new times without sending anything.
	for rel, entry := range out.entries {
		if !slices.ContainsFunc(out.changes, func(c crewTeamChange) bool { return c.Path == rel }) {
			b.Files[rel] = entry
		}
	}
	sort.Slice(out.changes, func(i, j int) bool { return out.changes[i].Path < out.changes[j].Path })
	return out, nil
}

// crewTeamUpload sends this copy's changes in batches: plan, the missing chunks in frames of at most
// crewTeamChunkBatch bytes (read from the files again and checked), then commit.
func crewTeamUpload(ctx context.Context, owner string, c desktopConnection, route, query string, b *crewTeamLocal, scan crewTeamScanResult) error {
	var sent int64
	for start := 0; start < len(scan.changes); start += crewTeamCommitBatch {
		batch := scan.changes[start:min(start+crewTeamCommitBatch, len(scan.changes))]
		request := crewTeamSyncRequest{Client: b.Client, TransferID: uuid.NewString(), Changes: batch, OnPurpose: slices.Clone(b.SyncAnyway)}
		var plan struct {
			Missing []string `json:"missing"`
		}
		if e := crewTeamRemoteJSON(ctx, owner, c, http.MethodPost, route+"/sync/plan"+query, request, &plan); e != nil {
			return crewTeamStep("plan", e)
		}
		for i := 0; i < len(plan.Missing); {
			var frames bytes.Buffer
			for ; i < len(plan.Missing) && (frames.Len() == 0 || int64(frames.Len())+crewTeamChunkMax <= crewTeamChunkBatch); i++ {
				id := plan.Missing[i]
				source, ok := scan.sources[id]
				if !ok {
					return crewTeamStep("send", fmt.Errorf("the server asked for a chunk this copy did not send"))
				}
				data, err := crewTeamReadSource(b.Root, source, id)
				if err != nil {
					return crewTeamStep("send", err)
				}
				if err = crewTeamWriteFrame(&frames, id, data); err != nil {
					return crewTeamStep("send", err)
				}
				sent += int64(len(data))
			}
			resp, err := crewTeamRemoteDo(ctx, owner, c, http.MethodPut, route+"/chunks"+query, "application/octet-stream", &frames)
			if err != nil {
				return crewTeamStep("send", err)
			}
			resp.Body.Close()
			crewTeamRoundBytes(ctx, sent)
		}
		var receipt crewTeamReceipt
		if e := crewTeamRemoteJSON(ctx, owner, c, http.MethodPost, route+"/sync/commit"+query, request, &receipt); e != nil {
			return crewTeamStep("commit", e)
		}
		for _, ch := range batch {
			if entry, ok := scan.entries[ch.Path]; ok && ch.After != nil {
				b.Files[ch.Path] = entry
			} else {
				delete(b.Files, ch.Path)
			}
		}
		// Taken back from Sync it anyway: the file stays here and is no longer the team's.
		b.StopSyncing = slices.DeleteFunc(b.StopSyncing, func(p string) bool { _, kept := b.Files[p]; return !kept })
		b.FolderOnPurpose = sortedOnPurpose(crewTeamOnPurpose{}.with(b.FolderOnPurpose).with(b.SyncAnyway))
	}
	return nil
}

// crewTeamReadSource reads one chunk back from this copy and checks it is still the same bytes.
func crewTeamReadSource(root string, source crewTeamChunkSource, id string) ([]byte, error) {
	f, err := os.Open(filepath.Join(root, filepath.FromSlash(source.path)))
	if err != nil {
		return nil, fmt.Errorf("a file changed while it was sent; it goes next round")
	}
	defer f.Close()
	data := make([]byte, source.size)
	if _, err = f.ReadAt(data, source.offset); err != nil {
		return nil, fmt.Errorf("a file changed while it was sent; it goes next round")
	}
	if sum := sha256.Sum256(data); hex.EncodeToString(sum[:]) != id {
		return nil, fmt.Errorf("a file changed while it was sent; it goes next round")
	}
	return data, nil
}

// crewTeamDownload brings one page of the team's changes into this copy. A file changed here since the copy was last
// in step is left alone (it goes up next round; the server keeps both). Each needed chunk is copied from this copy's
// own unchanged files or fetched, into private staging, before any file is written. It reports whether a file was
// left alone.
func crewTeamDownload(ctx context.Context, owner string, c desktopConnection, route, query, dir string, b *crewTeamLocal, files []crewTeamRemoteFile) (bool, error) {
	op := b.onPurpose()
	unchanged := func(rel string) bool {
		known, had := b.Files[rel]
		info, err := os.Lstat(filepath.Join(b.Root, filepath.FromSlash(rel)))
		if !had {
			return os.IsNotExist(err)
		}
		return err == nil && info.Mode().IsRegular() && info.Size() == known.Size && info.ModTime().UnixNano() == known.Mtime
	}
	skipped := false
	// Removals first, so a folder that became a file (or a file that became a folder) is out of the way.
	for _, f := range files {
		if _, had := b.Files[f.Path]; f.Version != nil || !had {
			continue
		}
		if !unchanged(f.Path) {
			skipped = true
			continue
		}
		if err := removeCrewTeamFile(b.Root, f.Path); err != nil {
			return skipped, crewTeamStep("write", err)
		}
		delete(b.Files, f.Path)
	}
	writes := []crewTeamRemoteFile{}
	for _, f := range files {
		known, had := b.Files[f.Path]
		if f.Version == nil || had && known.Version.Hash == f.Version.Hash {
			continue
		}
		if !unchanged(f.Path) {
			skipped = true
			continue
		}
		writes = append(writes, f)
	}
	if len(writes) == 0 {
		return skipped, nil
	}
	stage := filepath.Join(dir, "incoming")
	if err := os.MkdirAll(stage, 0o700); err != nil {
		return skipped, err
	}
	defer os.RemoveAll(stage)
	// Chunks this copy already holds, in files that are still as last synced.
	local := map[string]crewTeamChunkSource{}
	for rel, known := range b.Files {
		if !unchanged(rel) {
			continue
		}
		var offset int64
		for _, ch := range known.Version.Chunks {
			if _, ok := local[ch.ID]; !ok {
				local[ch.ID] = crewTeamChunkSource{path: rel, offset: offset, size: ch.Len}
			}
			offset += ch.Len
		}
	}
	need, fetch, seen := []crewTeamChunk{}, []crewTeamChunk{}, map[string]bool{}
	var total int64
	for _, f := range writes {
		for _, ch := range f.Version.Chunks {
			if !seen[ch.ID] {
				seen[ch.ID] = true
				need = append(need, ch)
			}
		}
	}
	for _, ch := range need {
		if source, ok := local[ch.ID]; ok {
			if data, err := crewTeamReadSource(b.Root, source, ch.ID); err == nil {
				if err = os.WriteFile(filepath.Join(stage, ch.ID), data, 0o600); err != nil {
					return skipped, err
				}
				continue
			}
		}
		fetch = append(fetch, ch)
		total += ch.Len
	}
	roundCtx := startCrewTeamRound(ctx, dir, b.Project, "download", len(writes), total, 0)
	err := crewTeamFetchChunks(roundCtx, owner, c, route, query, stage, fetch)
	if err == nil {
		for _, f := range writes {
			// Edited here while the chunks arrived: keep the local edit; it goes up next round.
			if !unchanged(f.Path) {
				skipped = true
				continue
			}
			reader, writer := io.Pipe()
			go func(v crewTeamFileVersion) {
				var copyErr error
				for _, ch := range v.Chunks {
					data, err := os.ReadFile(filepath.Join(stage, ch.ID))
					if err != nil {
						copyErr = err
						break
					}
					if _, copyErr = writer.Write(data); copyErr != nil {
						break
					}
				}
				writer.CloseWithError(copyErr)
			}(*f.Version)
			err = writeCrewTeamFile(ctx, b.Root, f.Path, reader, *f.Version.file(), op)
			reader.Close()
			if err != nil {
				err = crewTeamStep("write", err)
				break
			}
			info, statErr := os.Lstat(filepath.Join(b.Root, filepath.FromSlash(f.Path)))
			if statErr != nil {
				err = crewTeamStep("write", statErr)
				break
			}
			b.Files[f.Path] = crewTeamLocalFile{Size: info.Size(), Mtime: info.ModTime().UnixNano(), Version: *f.Version}
			log.Printf("[CREW_TEAM_LOCAL] project=%s actor=%s client=%s path=%q event=received", b.Project, b.RemoteUser, b.Client, f.Path)
		}
	}
	endCrewTeamRound(dir, err)
	return skipped, err
}

// crewTeamFetchChunks asks the server for chunks until all have arrived (each reply holds at most crewTeamChunkBatch
// bytes) and stages each, checked, by its id.
func crewTeamFetchChunks(ctx context.Context, owner string, c desktopConnection, route, query, stage string, chunks []crewTeamChunk) error {
	var received int64
	for len(chunks) > 0 {
		body, _ := json.Marshal(map[string]any{"chunks": chunks})
		resp, err := crewTeamRemoteDo(ctx, owner, c, http.MethodPost, route+"/chunks"+query, "application/json", bytes.NewReader(body))
		if err != nil {
			return crewTeamStep("receive", err)
		}
		got := map[string]bool{}
		err = crewTeamReadFrames(resp.Body, func(id string, data []byte) error {
			if sum := sha256.Sum256(data); hex.EncodeToString(sum[:]) != id {
				return fmt.Errorf("a received chunk does not match its id")
			}
			got[id] = true
			received += int64(len(data))
			crewTeamRoundBytes(ctx, received)
			return os.WriteFile(filepath.Join(stage, id), data, 0o600)
		})
		resp.Body.Close()
		if err != nil {
			return crewTeamStep("receive", err)
		}
		if len(got) == 0 {
			return crewTeamStep("receive", fmt.Errorf("the server did not send the shared files' content"))
		}
		chunks = slices.DeleteFunc(chunks, func(ch crewTeamChunk) bool { return got[ch.ID] })
	}
	return nil
}

// crewTeamStepError names the sync step that failed, for the log (the shown text stays the plain one).
type crewTeamStepError struct {
	step string
	err  error
}

func (e crewTeamStepError) Error() string { return e.err.Error() }
func (e crewTeamStepError) Unwrap() error { return e.err }

func crewTeamStep(step string, err error) error {
	if err == nil {
		return nil
	}
	return crewTeamStepError{step: step, err: err}
}

func crewTeamStepOf(err error) string {
	var step crewTeamStepError
	if errors.As(err, &step) {
		return step.step
	}
	return "-"
}

func crewTeamPlainSyncError(err error) string {
	text := err.Error()
	for _, known := range []struct{ match, say string }{
		{"cannot reach your company's server", "your company's server can't be reached"},
		{"the server took too long", "the server took too long; retrying"},
		{"the shared folder moved", "the shared folder moved on this Mac; pick it again in the Team tab"},
		{"belongs to a different saved account", "this folder belongs to a different sign-in"},
		{"the shared folder changed", "the Team changed what it shares; this copy is no longer shared"},
		{"shared folder access was removed", "you no longer have the shared folder; its files stay on this Mac"},
		{"ask the owner to allow shared folder editing", "the shared folder is not shared with you"},
		{"transfer refused", "the server refused the transfer; check that you are still in the Team"},
		{"exceeds 256 MiB", "what the team shares is over 256 MB or 10,000 files"},
		{"outside what the team shares", "a file is outside what the team shares"},
		{"differ only by letter case", "two files differ only by capital letters; rename one"},
		{"context deadline exceeded", "it took too long"},
	} {
		if strings.Contains(text, known.match) {
			return known.say
		}
	}
	return "something went wrong on this Mac"
}
