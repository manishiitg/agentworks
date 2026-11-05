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
	Files    map[string]crewTeamLocalFile `json:"files"`
	Revision uint64                       `json:"revision"`
	// Downloaded: this copy has received the whole team folder once. Until then it only downloads and changes nothing
	// on the team (review 5 Nov: a new member's blank Crew setup replaced the owner's).
	Downloaded bool   `json:"downloaded,omitempty"`
	LastSynced string `json:"last_synced,omitempty"`
	Status     string `json:"status"`
	Error      string `json:"error,omitempty"`
	// "Sync it anyway" (owner, 5 Nov): this person's own choice of files that look like they hold passwords or keys,
	// by exact path; StopSyncing are ones taken back, removed from the shared folder on the next upload (the files
	// stay here). FolderOnPurpose is what the shared folder holds on purpose, from the server's last answer.
	SyncAnyway      []string `json:"sync_anyway,omitempty"`
	StopSyncing     []string `json:"stop_syncing,omitempty"`
	FolderOnPurpose []string `json:"folder_on_purpose,omitempty"`
	// TooLarge are files over the per-file limit (64 MiB) this copy leaves out, by path, for the sync status; the rest
	// of the folder still syncs.
	TooLarge []string `json:"too_large,omitempty"`
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
		// A member keeps the folder they chose for this Crew when the Team's shared folder changes (review 5 Nov #5).
		b.Root = previousCrewTeamMemberRoot(filepath.Dir(dir), b)
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

// previousCrewTeamMemberRoot is the folder a member's earlier copy of the same Team used (on the same saved sign-in),
// the most recent one that still exists.
func previousCrewTeamMemberRoot(parent string, b crewTeamLocal) string {
	records, _ := filepath.Glob(filepath.Join(parent, "*", "binding.json"))
	root, newest := "", time.Time{}
	for _, record := range records {
		old, err := readCrewTeamLocal(filepath.Dir(record))
		info, statErr := os.Stat(record)
		if err != nil || statErr != nil || old.Project != b.Project || old.Connection != b.Connection || old.RemoteUser != b.RemoteUser || old.Root == "" {
			continue
		}
		if dirInfo, err := os.Lstat(old.Root); err != nil || !dirInfo.IsDir() || dirInfo.Mode()&os.ModeSymlink != 0 {
			continue
		}
		if info.ModTime().After(newest) {
			root, newest = old.Root, info.ModTime()
		}
	}
	return root
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
						round, done := context.WithTimeout(ctx, 20*time.Minute)
						err = api.crewTeamMacRound(round, owner, c, dir, &b)
						done()
						if err != nil {
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

// crewTeamMacRound is one round of a Mac copy with its Crew's setup (crew_team_setup.go): this Crew's setup changes go
// up, then someone's setup edit that came down is applied. A copy writes its setup only once it has received the team
// folder (review 5 Nov: a new member's blank setup replaced the owner's).
func (api *StreamingAPI) crewTeamMacRound(ctx context.Context, owner string, c desktopConnection, dir string, b *crewTeamLocal) error {
	local := crewTeamLocalCrew(*b)
	api.reconcileCrewTeamSetup(ctx, owner, local, b.Root, dir, b.Downloaded)
	if err := syncCrewTeamLocal(ctx, owner, c, dir, b); err != nil {
		return err
	}
	api.reconcileCrewTeamSetup(ctx, owner, local, b.Root, dir, b.Downloaded)
	return nil
}

// syncCrewTeamLocal runs one round for a copy: one round per copy at a time, from the copy's record as it is on disk
// now. A caller's earlier read can be stale (another round finished meanwhile), and its bases would then revert a newer
// edit (review 5 Nov).
func syncCrewTeamLocal(ctx context.Context, owner string, c desktopConnection, dir string, b *crewTeamLocal) error {
	unlock, e := wf.LockWorkspace(ctx, dir)
	if e != nil {
		return e
	}
	defer unlock()
	if fresh, err := readCrewTeamLocal(dir); err == nil {
		*b = fresh
	} else if !os.IsNotExist(err) {
		return err
	}
	if e = syncCrewTeamRound(ctx, owner, c, dir, b); e != nil {
		b.Status = "pending"
		b.Error = crewTeamPlainSyncError(e)
		_ = saveCrewTeamLocal(dir, *b)
	}
	return e
}

func syncCrewTeamRound(ctx context.Context, owner string, c desktopConnection, dir string, b *crewTeamLocal) error {
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
	// A copy that has not yet received the team folder once only downloads.
	scan := crewTeamScanResult{}
	if b.Downloaded {
		if scan, e = crewTeamScan(ctx, b, team.Folder); e != nil {
			return crewTeamStep("scan", e)
		}
	}
	// An upload that fails (a refused file, the server busy) does not stop what the team changed from coming down;
	// the failure is reported once the download is done, and what did not go up goes next round.
	var uploadErr error
	if len(scan.changes) > 0 {
		roundCtx := startCrewTeamRound(ctx, dir, b.Project, "upload", len(scan.changes), 0, 0)
		uploadErr = crewTeamUpload(roundCtx, owner, c, route, query, b, scan)
		endCrewTeamRound(dir, uploadErr)
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
		if skipped {
			// A file changed here meanwhile: this copy stays at its revision, so the team's version comes again next
			// round, after this copy's change went up (review 5 Nov).
			pending = true
			if e = saveCrewTeamLocal(dir, *b); e != nil {
				return e
			}
			break
		}
		b.Revision = page.Through
		if !page.More && !b.Downloaded {
			// From now on this copy also sends its changes (a copy that already held the team's files adopted them).
			b.Downloaded = true
			log.Printf("[CREW_TEAM_LOCAL] project=%s client=%s event=first_download_done files=%d revision=%d", b.Project, b.Client, len(b.Files), b.Revision)
		}
		if e = saveCrewTeamLocal(dir, *b); e != nil {
			return e
		}
		if !page.More {
			break
		}
	}
	if uploadErr != nil {
		log.Printf("[CREW_TEAM_LOCAL] project=%s client=%s event=upload_failed_download_done revision=%d", b.Project, b.Client, b.Revision)
		return uploadErr
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
	ignored, err := crewTeamIgnored(ctx, b.Root)
	if err != nil {
		return out, err
	}
	op := b.onPurpose()
	// seen: files this round shares; present: every file name the walk met, spelled as on disk now (on a Mac's
	// case-insensitive disk a lookup of the old spelling still succeeds after a case-only rename); left: folders the
	// walk did not enter.
	seen, present, left := map[string]bool{}, map[string]bool{}, []string{}
	tooLarge := []string{}
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
				left = append(left, rel)
				return filepath.SkipDir
			}
			return nil
		}
		present[rel] = true
		if d.Type()&os.ModeSymlink != 0 || !d.Type().IsRegular() || !crewsync.Allowed(crewTeamKey(rel), false) || ignored[rel] || !share.holds(rel) || !crewTeamAllowed(rel, op) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if info.Size() > publication.MaxFileBytes {
			// Over the per-file limit: left out and named in the status, so it never blocks the rest (it stays here,
			// and the team's earlier version of it stays on the team).
			tooLarge = append(tooLarge, rel)
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
	// Removed here, or taken back from Sync it anyway (the file stays here): removed from the team. A file that is
	// still here but this copy leaves out (ignored, outside the share) is not this copy's to remove (review 5 Nov).
	b.TooLarge = tooLarge
	for rel, known := range b.Files {
		gone := !present[rel]
		if gone && slices.ContainsFunc(left, func(dir string) bool { return strings.HasPrefix(rel, dir+"/") }) {
			// Inside a folder the walk left out (its names were not read): only a file that is really gone.
			info, err := os.Lstat(filepath.Join(b.Root, filepath.FromSlash(rel)))
			gone = err != nil || !info.Mode().IsRegular()
		}
		if !seen[rel] && gone || slices.Contains(b.StopSyncing, rel) {
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
	// Removals go first, so no batch adds a name (docs/a.md) while its old spelling (Docs/a.md) is still on the team.
	sort.Slice(out.changes, func(i, j int) bool {
		if (out.changes[i].After == nil) != (out.changes[j].After == nil) {
			return out.changes[i].After == nil
		}
		return out.changes[i].Path < out.changes[j].Path
	})
	return out, nil
}

// crewTeamUpload sends this copy's changes in batches: plan, the missing chunks in frames of at most
// crewTeamChunkBatch bytes (read from the files again and checked), then commit.
func crewTeamUpload(ctx context.Context, owner string, c desktopConnection, route, query string, b *crewTeamLocal, scan crewTeamScanResult) error {
	// Every batch is planned first, so the round's total is the bytes the server lacks (review 5 Nov #11).
	type planned struct {
		request crewTeamSyncRequest
		batch   []crewTeamChange
		missing []string
	}
	plans := []planned{}
	var sent, total int64
	for start := 0; start < len(scan.changes); start += crewTeamCommitBatch {
		batch := scan.changes[start:min(start+crewTeamCommitBatch, len(scan.changes))]
		// The same change set retried (a commit whose reply was lost) carries the same transfer id, so the server's
		// receipt answers it instead of applying a stale base over a newer edit (review 5 Nov #7).
		request := crewTeamSyncRequest{Client: b.Client, TransferID: crewTeamChangeSetID(b.Client, b.Revision, batch), Changes: batch, OnPurpose: slices.Clone(b.SyncAnyway)}
		var plan struct {
			Missing []string `json:"missing"`
		}
		if e := crewTeamRemoteJSON(ctx, owner, c, http.MethodPost, route+"/sync/plan"+query, request, &plan); e != nil {
			return crewTeamStep("plan", e)
		}
		for _, id := range plan.Missing {
			total += scan.sources[id].size
		}
		plans = append(plans, planned{request: request, batch: batch, missing: plan.Missing})
	}
	crewTeamRoundTotal(ctx, total)
	for _, p := range plans {
		request, batch, plan := p.request, p.batch, struct{ Missing []string }{p.missing}
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
	// holds: the file here is exactly this version (read only when its size or time differ from what was recorded).
	holds := func(rel string, known crewTeamLocalFile) bool {
		info, err := os.Lstat(filepath.Join(b.Root, filepath.FromSlash(rel)))
		if err != nil || !info.Mode().IsRegular() {
			return false
		}
		if info.Size() == known.Size && info.ModTime().UnixNano() == known.Mtime {
			return true
		}
		if info.Size() != known.Version.Size || crewTeamHashFile(b.Root, rel) != known.Version.Hash {
			return false
		}
		b.Files[rel] = crewTeamLocalFile{Size: info.Size(), Mtime: info.ModTime().UnixNano(), Version: known.Version} // touched, not changed
		return true
	}
	// unchanged: the file here is as this copy last had it in step with the team (absent if it never had it).
	unchanged := func(rel string) bool {
		known, had := b.Files[rel]
		if !had {
			_, err := os.Lstat(filepath.Join(b.Root, filepath.FromSlash(rel)))
			return os.IsNotExist(err)
		}
		return holds(rel, known)
	}
	skipped := false
	// Removals first, so a folder that became a file (or a file that became a folder) is out of the way.
	for _, f := range files {
		if _, had := b.Files[f.Path]; f.Version != nil || !had {
			continue
		}
		if !unchanged(f.Path) {
			if !b.Downloaded {
				delete(b.Files, f.Path) // first download: the file here stays and goes up as this copy's
				continue
			}
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
		if holds(f.Path, crewTeamLocalFile{Size: -1, Version: *f.Version}) {
			continue // already here (adopted as in step)
		}
		if !unchanged(f.Path) {
			if !b.Downloaded {
				// First download into a folder that already has this file: it stays, recorded against the team's
				// version, so it goes up as an edit of it (the team's version stays in history), never silently lost.
				b.Files[f.Path] = crewTeamLocalFile{Size: -1, Version: *f.Version}
				continue
			}
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
	moved, err := crewTeamFetchChunks(roundCtx, owner, c, route, query, stage, fetch)
	if err == nil {
		for _, f := range writes {
			// Its version moved on while this page was read (review 5 Nov #8): the newer version comes in a later page.
			if slices.ContainsFunc(f.Version.Chunks, func(ch crewTeamChunk) bool { return moved[ch.ID] }) {
				log.Printf("[CREW_TEAM_LOCAL] project=%s client=%s path=%q event=moved_on", b.Project, b.Client, f.Path)
				continue
			}
			// Edited here while the chunks arrived: keep the local edit; it goes up next round.
			if !unchanged(f.Path) {
				if !b.Downloaded {
					b.Files[f.Path] = crewTeamLocalFile{Size: -1, Version: *f.Version}
					continue
				}
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

// crewTeamHashFile is the sha256 of a file in this copy, or "" when it cannot be read.
func crewTeamHashFile(root, rel string) string {
	f, err := os.Open(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return ""
	}
	defer f.Close()
	sum := sha256.New()
	if _, err = io.Copy(sum, f); err != nil {
		return ""
	}
	return hex.EncodeToString(sum.Sum(nil))
}

// crewTeamFetchChunks asks the server for chunks until all have arrived (each reply holds at most crewTeamChunkBatch
// bytes) and stages each, checked, by its id. It returns the chunks the server no longer serves: their file's version
// moved on since the page was read.
func crewTeamFetchChunks(ctx context.Context, owner string, c desktopConnection, route, query, stage string, chunks []crewTeamChunk) (map[string]bool, error) {
	var received int64
	moved := map[string]bool{}
	for len(chunks) > 0 {
		body, _ := json.Marshal(map[string]any{"chunks": chunks})
		resp, err := crewTeamRemoteDo(ctx, owner, c, http.MethodPost, route+"/chunks"+query, "application/json", bytes.NewReader(body))
		if err != nil {
			return moved, crewTeamStep("receive", err)
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
			return moved, crewTeamStep("receive", err)
		}
		if len(got) == 0 {
			for _, ch := range chunks {
				moved[ch.ID] = true
			}
			return moved, nil
		}
		chunks = slices.DeleteFunc(chunks, func(ch crewTeamChunk) bool { return got[ch.ID] })
	}
	return moved, nil
}

// crewTeamChangeSetID names one batch of changes from one copy at the revision it had received: the same batch retried
// has the same id; the same edit made again later is sent from a later revision, so it is a new transfer.
func crewTeamChangeSetID(client string, revision uint64, batch []crewTeamChange) string {
	var key strings.Builder
	key.WriteString(fmt.Sprintf("%s\x00%d", client, revision))
	for _, ch := range batch {
		after := ""
		if ch.After != nil {
			after = ch.After.Hash
		}
		key.WriteString("\x00" + ch.Path + "\x00" + ch.Before + "\x00" + after)
	}
	return crewTransferDigest(key.String())
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

// crewTeamTooLargeNotice names the files left out for being over 64 MB (the first few), for the sync status.
func crewTeamTooLargeNotice(paths []string) string {
	shown := paths[:min(len(paths), 5)]
	more := ""
	if len(paths) > len(shown) {
		more = fmt.Sprintf(" and %d more", len(paths)-len(shown))
	}
	return "Not synced, over 64 MB each: " + strings.Join(shown, ", ") + more + ". Everything else syncs."
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
