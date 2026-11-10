package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"syscall"
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
	// Files is what this copy last had in step with the team, including the
	// identity/stat stamp observed while reading that version's contents.
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
	Stamp   crewsync.Stamp `json:"stamp"`
	ReadAt  time.Time      `json:"read_at"`
	Version chunkedFile    `json:"version"`
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
			pushed := true // every server with a Team here has a live relay socket to tell this Mac of changes
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
					if _, live := desktopRelayLive.Load(c.ID); len(out.Teams) > 0 && !live {
						pushed = false
					}
					for _, t := range out.Teams {
						// The owner's Mac follows its Crew's Team record, changed from anywhere (crew_team_apply.go).
						if t.OwnerID == c.User.ID {
							if local := crewLocalProjectForTeam(owner, c, t.ProjectID); local != "" && readCrewTeamState(local).TeamOn != t.On {
								api.applyCrewTeamOnMac(ctx, local, t)
							}
						}
						// The owner's Mac Crew's dashboards reach the Team (crew_mac_dashboards.go).
						if t.OwnerID == c.User.ID && t.On {
							if local := crewLocalProjectForTeam(owner, c, t.ProjectID); local != "" {
								if err := api.sendCrewMacDashboards(ctx, owner, c, local); err != nil {
									log.Printf("[CREW_MAC_DASHBOARDS] project=%s event=send_failed reason=%q", local, err)
								}
							}
						}
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
			case <-time.After(crewTeamCheckAfter(pushed)):
			case <-crewTeamWake: // a watched copy changed (crew_team_watch.go), or the server said a teammate's did
			}
		}
	}()
	return cancel
}

// crewTeamCheckAfter is the timed check for teammates' changes and Team records. When every server with a Team here
// has this Mac's relay socket open, each tells the Mac of a change at once (crew_team_push.go) and the check only
// catches a missed note; otherwise it stays quick.
func crewTeamCheckAfter(pushed bool) time.Duration {
	if pushed {
		return 60 * time.Second
	}
	return 5 * time.Second
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
	// A copy an agent works in, written before its files had group access, is repaired once.
	if marker := filepath.Join(dir, "modes-repaired"); crewTeamGroupPolicy(b.Root) && !fileExists(marker) {
		started := time.Now()
		changed, repairErr := repairCrewTeamCopyModes(b.Root)
		log.Printf("[CREW_TEAM_LOCAL] project=%s client=%s event=modes_repaired changed=%d ms=%d err=%v", b.Project, b.Client, changed, time.Since(started).Milliseconds(), repairErr)
		if repairErr == nil {
			_ = os.WriteFile(marker, nil, 0o600)
		}
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
	var uploadErr error
	// Only what a watcher saw change is read, with a full scan now and then (crew_team_watch.go).
	scan := crewTeamScanResult{}
	if b.Downloaded {
		started, key := time.Now(), crewTeamScanKey(*b, team.Folder)
		watch := crewTeamWatchFor(dir, b.Root)
		plan := watch.take(key)
		if !plan.idle() {
			scan, e = crewTeamScanChanged(ctx, b, team.Folder, plan.changed, plan.rules)
		}
		// Settle metadata after a touch/racy recheck without advancing any
		// changed version's acknowledgement before the upload succeeds.
		if e == nil {
			for rel, entry := range scan.entries {
				if known, ok := b.Files[rel]; ok && crewTeamSameVersion(known.Version, entry.Version) {
					b.Files[rel] = entry
				}
			}
		}
		// An upload that fails (a refused file, the server busy) does not stop what the team changed from coming
		// down; the failure is reported once the download is done, and what did not go up goes next round.
		var chunks int
		var saveErr error
		if e == nil && len(scan.changes) > 0 {
			roundCtx := startCrewTeamRound(ctx, dir, b.Project, "upload", len(scan.changes), 0, 0)
			chunks, uploadErr = crewTeamUpload(roundCtx, owner, c, route, query, b, scan)
			endCrewTeamRound(dir, uploadErr)
			saveErr = saveCrewTeamLocal(dir, *b)
		}
		var retryErr error
		if scan.unstable > 0 {
			retryErr = errCrewTeamFileChanged
		}
		watch.done(b.Project, key, plan, scan, errors.Join(e, uploadErr, saveErr, retryErr))
		if !plan.idle() {
			log.Printf("[CREW_TEAM_LOCAL] project=%s client=%s event=round scan=%s watched=%d stated=%d names=%d reused=%d read_files=%d read_bytes=%d unstable=%d changes=%d chunks_sent=%d ms=%d",
				b.Project, b.Client, plan.mode(), plan.watched, scan.stated, scan.names, scan.reused, scan.readFiles, scan.readBytes, scan.unstable, len(scan.changes), chunks, time.Since(started).Milliseconds())
		}
		if e != nil {
			return crewTeamStep("scan", e)
		}
		if saveErr != nil {
			return saveErr
		}
	}
	// 2. What changed on the team comes down, page by page.
	pending := scan.unstable > 0
	for {
		var page crewTeamChangesReply
		if e = crewTeamRemoteJSON(ctx, owner, c, http.MethodPost, route+"/sync/changes"+query, crewTeamSyncRequest{Client: b.Client, Since: b.Revision}, &page); e != nil {
			return crewTeamStep("changes", e)
		}
		if page.CatchUp {
			// Use the existing first-download reconciliation: local edited bytes
			// stay here, against the server's current base, for the next upload.
			b.Downloaded = false
			pending = true // reconciliation may preserve edits made after the upload scan
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
		if page.CatchUp {
			b.Status = "pending"
			if e = saveCrewTeamLocal(dir, *b); e != nil {
				return e
			}
			if errors.Is(uploadErr, errCrewTeamHistoryCatchUp) {
				uploadErr = nil
			}
			// Reconcile can preserve local edits without a filesystem change event.
			// Scan them on the next round before reporting this copy up to date.
			watch := crewTeamWatchFor(dir, b.Root)
			watch.mu.Lock()
			watch.full = true
			watch.mu.Unlock()
			crewTeamWakeSoon()
			log.Printf("[CREW_TEAM_LOCAL] project=%s client=%s event=catch_up_applied revision=%d", b.Project, b.Client, b.Revision)
			continue // only a subsequent normal delta may report synchronized
		}
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

type crewTeamScanResult struct {
	changes   []crewTeamChange
	entries   map[string]crewTeamLocalFile // the new state of each changed path (absent for a removal)
	sources   map[string]chunkSource
	dirs      []string // folders the scan entered: what a watcher watches ("" is the copy's folder)
	stated    int      // files whose size and time were read
	reused    int      // stable file versions reused without reading contents
	readFiles int
	readBytes int64
	unstable  int  // retry these files next round without blocking other transfers
	names     int  // names in the folders the scan listed
	rules     bool // the folder has a .gitignore (known after a full scan)
	respell   bool // a changed folder is spelled differently on disk now: only a full scan reads it right
}

// crewTeamScan finds what changed in this copy since it was last in step: a file with a trusted unchanged stamp
// is not read; any other is chunked. Only what the share holds is looked at (crewTeamAllowed, Sync it
// anyway, Whole Crew or the chosen folders, .gitignore), as the old capture did.
func crewTeamScan(ctx context.Context, b *crewTeamLocal, share crewTeamFolder) (crewTeamScanResult, error) {
	return crewTeamScanChanged(ctx, b, share, nil, false)
}

// crewTeamScanChanged is crewTeamScan for the paths a watcher saw change (crew_team_watch.go): it reads the folder
// each is in, and a path marked true (made, removed or renamed: maybe a folder) with all it holds. changed nil reads
// the whole copy. rules is whether the folder had a .gitignore at its last full scan.
func crewTeamScanChanged(ctx context.Context, b *crewTeamLocal, share crewTeamFolder, changed map[string]bool, rules bool) (crewTeamScanResult, error) {
	out := crewTeamScanResult{entries: map[string]crewTeamLocalFile{}, sources: map[string]chunkSource{}}
	op := b.onPurpose()
	full := changed == nil
	enters := func(rel string) bool { return crewsync.Allowed(crewTeamKey(rel), true) && share.walks(rel) }
	// targets: folder -> whether everything under it is read (deep), or only its own files.
	targets := map[string]bool{"": full}
	if !full {
		delete(targets, "")
		for p, all := range changed {
			if p = strings.Trim(path.Clean("/"+p), "/"); p == "" {
				targets[""] = targets[""] || all
				continue
			}
			if _, ok := targets[crewTeamParent(p)]; !ok {
				targets[crewTeamParent(p)] = false // its own files only
			}
			if all {
				targets[p] = true
			}
		}
	}
	// present: every file name the listing met, spelled as on disk now (on a Mac's case-insensitive disk a lookup of
	// the old spelling still succeeds after a case-only rename); listed: folders whose names were all read, and deep
	// ones with all below them; left: folders a deep listing did not enter.
	type file struct {
		rel string
		d   fs.DirEntry
	}
	files := []file{}
	present, listed, deep, left := map[string]bool{}, map[string]bool{}, map[string]bool{}, []string{}
	names := map[string]map[string]bool{} // a folder's exact entry names, for spelling checks
	var list func(rel string, recurse bool) error
	list = func(rel string, recurse bool) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if listed[rel] && (deep[rel] || !recurse) {
			return nil
		}
		entries, err := os.ReadDir(filepath.Join(b.Root, filepath.FromSlash(rel)))
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
				listed[rel], deep[rel] = true, true // gone (or a file): what was under it is checked below
				return nil
			}
			return err
		}
		again := listed[rel]
		if !again {
			out.names += len(entries)
		}
		listed[rel] = true
		deep[rel] = deep[rel] || recurse
		out.dirs = append(out.dirs, rel)
		names[rel] = map[string]bool{}
		for _, d := range entries {
			child := path.Join(rel, d.Name())
			names[rel][d.Name()] = true
			if d.IsDir() {
				if !recurse {
					continue
				}
				if !enters(child) {
					left = append(left, child)
					continue
				}
				if err := list(child, true); err != nil {
					return err
				}
				continue
			}
			if !again {
				present[child] = true
				files = append(files, file{child, d})
			}
		}
		return nil
	}
	// spelled: every name in rel is on disk spelled exactly so now (a lookup by another spelling succeeds on a Mac).
	var spelled func(rel string) bool
	spelled = func(rel string) bool {
		if rel == "" {
			return true
		}
		parent := crewTeamParent(rel)
		if !spelled(parent) {
			return false
		}
		if names[parent] == nil {
			entries, err := os.ReadDir(filepath.Join(b.Root, filepath.FromSlash(parent)))
			if err != nil {
				return false
			}
			names[parent] = map[string]bool{}
			for _, d := range entries {
				names[parent][d.Name()] = true
			}
		}
		return names[parent][path.Base(rel)]
	}
	// inside: rel and every folder above it are folders the share reads.
	inside := func(rel string) bool {
		for p := rel; p != ""; p = crewTeamParent(p) {
			if !enters(p) {
				return false
			}
		}
		return true
	}
	order := make([]string, 0, len(targets))
	for rel := range targets {
		order = append(order, rel)
	}
	sort.Strings(order) // a parent before what is inside it
	for _, rel := range order {
		if !spelled(rel) {
			if _, err := os.Lstat(filepath.Join(b.Root, filepath.FromSlash(rel))); err == nil {
				out.respell = true // found only by another spelling: a full scan reads the names as they are now
				return out, nil
			}
			listed[rel], deep[rel] = true, true // gone: what was under it is checked below
			continue
		}
		if !inside(rel) {
			continue
		}
		if err := list(rel, targets[rel]); err != nil {
			return out, err
		}
	}
	var ignored map[string]bool
	var err error
	if full {
		ignored, out.rules, err = crewTeamIgnored(ctx, b.Root)
	} else {
		out.rules = rules
		ignored = map[string]bool{}
		if rules {
			paths := make([]string, 0, len(files))
			for _, f := range files {
				paths = append(paths, f.rel)
			}
			ignored, err = crewTeamCheckIgnore(ctx, b.Root, paths)
		}
	}
	if err != nil {
		return out, err
	}
	// covered: a file of the copy this scan read the folder of, so its absence means it is gone.
	covered := func(rel string) bool {
		if listed[crewTeamParent(rel)] {
			return true
		}
		for dir := crewTeamParent(rel); dir != ""; {
			if dir = crewTeamParent(dir); deep[dir] {
				return true
			}
		}
		return false
	}
	seen := map[string]bool{} // files this round shares
	tooLarge := []string{}
	for _, f := range files {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		crewTeamScanFile(b, op, &out, f.rel, f.d, ignored, share, seen, &tooLarge)
	}
	// Removed here, or taken back from Sync it anyway (the file stays here): removed from the team. A file that is
	// still here but this copy leaves out (ignored, outside the share) is not this copy's to remove (review 5 Nov).
	for _, rel := range b.TooLarge {
		if !covered(rel) {
			tooLarge = append(tooLarge, rel) // a folder this round did not read keeps its notice
		}
	}
	b.TooLarge = tooLarge
	for rel, known := range b.Files {
		if !covered(rel) && !slices.Contains(b.StopSyncing, rel) {
			continue
		}
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

// crewTeamParent is the folder rel is in, "" for the copy's own folder.
func crewTeamParent(rel string) string {
	if dir := path.Dir(rel); dir != "." {
		return dir
	}
	return ""
}

// crewTeamScanFile reuses only a stable full stamp, including change-time,
// identity and mode; preserving a file's size/mtime cannot hide an edit.
func crewTeamScanFile(b *crewTeamLocal, op crewTeamOnPurpose, out *crewTeamScanResult, rel string, d fs.DirEntry, ignored map[string]bool, share crewTeamFolder, seen map[string]bool, tooLarge *[]string) {
	if d.Type()&os.ModeSymlink != 0 || !d.Type().IsRegular() || !crewsync.Allowed(crewTeamKey(rel), false) || ignored[rel] || !share.holds(rel) || !crewTeamAllowed(rel, op) {
		return
	}
	info, err := d.Info()
	out.stated++
	if err != nil {
		return
	}
	if info.Size() > publication.MaxFileBytes {
		// Over the per-file limit: left out and named in the status, so it never blocks the rest (it stays here,
		// and the team's earlier version of it stays on the team).
		*tooLarge = append(*tooLarge, rel)
		return
	}
	seen[rel] = true
	known, had := b.Files[rel]
	if had && known.Stamp.Reusable(crewsync.StampOf(info), known.ReadAt) {
		out.reused++
		return
	}
	entry, sources, err := readCrewTeamVersion(b.Root, rel, op, known.Version)
	if err != nil {
		if errors.Is(err, errCrewTeamFileChanged) {
			out.unstable++ // watcher.done retries; this file's acknowledged version stays unchanged
		}
		return // keep transferring other files, including teammates' downloads
	}
	for id, source := range sources {
		if _, ok := out.sources[id]; !ok {
			out.sources[id] = source
		}
	}
	out.readFiles++
	out.readBytes += entry.Version.Size
	out.entries[rel] = entry
	if had && crewTeamSameVersion(known.Version, entry.Version) {
		return // touched/racy, not changed: settle its stamp after the scan
	}
	out.changes = append(out.changes, crewTeamChange{Path: rel, Before: known.Version.Hash, After: &entry.Version})
}

// crewTeamUpload sends this copy's changes in batches: plan, the missing chunks in frames of at most
// chunkBatchBytes bytes (read from the files again and checked), then commit.
// It returns how many chunks it sent.
func crewTeamUpload(ctx context.Context, owner string, c desktopConnection, route, query string, b *crewTeamLocal, scan crewTeamScanResult) (int, error) {
	chunks, started := 0, time.Now()
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
		request := crewTeamSyncRequest{Client: b.Client, Since: b.Revision, TransferID: crewTeamChangeSetID(b.Client, b.Revision, batch), Changes: batch, OnPurpose: slices.Clone(b.SyncAnyway)}
		var plan struct {
			Missing []string `json:"missing"`
		}
		if e := crewTeamRemoteJSON(ctx, owner, c, http.MethodPost, route+"/sync/plan"+query, request, &plan); e != nil {
			return chunks, crewTeamStep("plan", e)
		}
		for _, id := range plan.Missing {
			total += scan.sources[id].size
		}
		plans = append(plans, planned{request: request, batch: batch, missing: plan.Missing})
	}
	crewTeamRoundTotal(ctx, total)
	for _, p := range plans {
		request, batch := p.request, p.batch
		base := sent
		n, moved, err := sendMissingChunks(ctx, owner, c, route+"/chunks"+query, b.Root, scan.sources, p.missing, func(s int64) { crewTeamRoundBytes(ctx, base+s) })
		chunks, sent = chunks+n, sent+moved
		if errors.Is(err, errChunkNotOffered) {
			return chunks, crewTeamStep("send", fmt.Errorf("the server asked for a chunk this copy did not send"))
		} else if err != nil {
			return chunks, crewTeamStep("send", err)
		}
		var receipt crewTeamReceipt
		if e := crewTeamRemoteJSON(ctx, owner, c, http.MethodPost, route+"/sync/commit"+query, request, &receipt); e != nil {
			return chunks, crewTeamStep("commit", e)
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
	afters := []chunkedFile{}
	for _, ch := range scan.changes {
		if ch.After != nil {
			afters = append(afters, *ch.After)
		}
	}
	logChunkTransfer("team", b.Project, "up", len(scan.changes), distinctChunks(afters), chunks, sent, started)
	return chunks, nil
}

// crewTeamDownloadBatch is how many files of a page are fetched and written before the copy's record is saved.
const crewTeamDownloadBatch = 200

// crewTeamDownload brings one page of the team's changes into this copy. A file changed here since the copy was last
// in step is left alone (it goes up next round; the server keeps both). Each needed chunk is copied from this copy's
// own unchanged files or fetched, into private staging, before any file is written. It reports whether a file was
// left alone.
func crewTeamDownload(ctx context.Context, owner string, c desktopConnection, route, query, dir string, b *crewTeamLocal, files []crewTeamRemoteFile) (bool, error) {
	op := b.onPurpose()
	// holds reuses only a trusted stamp; changed identities/modes or racy
	// timestamps require a stable content read before overwriting local work.
	holds := func(rel string, known crewTeamLocalFile) bool {
		info, err := os.Lstat(filepath.Join(b.Root, filepath.FromSlash(rel)))
		if err != nil || !info.Mode().IsRegular() {
			return false
		}
		if known.Stamp.Reusable(crewsync.StampOf(info), known.ReadAt) {
			return true
		}
		if info.Size() != known.Version.Size {
			return false
		}
		entry, _, err := readCrewTeamVersion(b.Root, rel, op, known.Version)
		if err != nil || !crewTeamSameVersion(entry.Version, known.Version) {
			return false
		}
		b.Files[rel] = entry // touched/racy, not changed
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
		if f.Version == nil || had && crewTeamSameVersion(known.Version, *f.Version) {
			continue
		}
		if holds(f.Path, crewTeamLocalFile{Version: *f.Version}) {
			continue // already here (adopted as in step)
		}
		if !unchanged(f.Path) {
			if !b.Downloaded {
				// First download into a folder that already has this file: it stays, recorded against the team's
				// version, so it goes up as an edit of it (the team's version stays in history), never silently lost.
				b.Files[f.Path] = crewTeamLocalFile{Version: *f.Version}
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
	local := map[string]chunkSource{}
	for rel, known := range b.Files {
		if !unchanged(rel) {
			continue
		}
		var offset int64
		for _, ch := range known.Version.Chunks {
			if _, ok := local[ch.ID]; !ok {
				local[ch.ID] = chunkSource{path: rel, offset: offset, size: ch.Len}
			}
			offset += ch.Len
		}
	}
	// The page goes in batches, each fetched, written and saved before the next: a round that runs out of time keeps
	// what it wrote, and the next round goes on from there (Showcase, 6 Nov: a 2,000-file page never finished).
	var total, doneBytes int64
	counted := map[string]bool{}
	for _, f := range writes {
		for _, ch := range f.Version.Chunks {
			if _, here := local[ch.ID]; !here && !counted[ch.ID] {
				counted[ch.ID] = true
				total += ch.Len
			}
		}
	}
	roundCtx := startCrewTeamRound(ctx, dir, b.Project, "download", len(writes), total, 0)
	receive := func(batch []crewTeamRemoteFile) error {
		if err := os.RemoveAll(stage); err != nil {
			return err
		}
		if err := os.MkdirAll(stage, 0o700); err != nil {
			return err
		}
		need, fetch, seen := []transferChunk{}, []transferChunk{}, map[string]bool{}
		var size int64
		for _, f := range batch {
			for _, ch := range f.Version.Chunks {
				if !seen[ch.ID] {
					seen[ch.ID] = true
					need = append(need, ch)
				}
			}
		}
		for _, ch := range need {
			if source, ok := local[ch.ID]; ok {
				if data, err := readChunkSource(b.Root, source, ch.ID); err == nil {
					if err = os.WriteFile(filepath.Join(stage, ch.ID), data, 0o600); err != nil {
						return err
					}
					continue
				}
			}
			fetch = append(fetch, ch)
			size += ch.Len
		}
		started := time.Now()
		moved, err := fetchChunks(roundCtx, owner, c, route, query, stage, fetch, func(received int64) { crewTeamRoundBytes(roundCtx, doneBytes+received) })
		if err != nil {
			return err
		}
		doneBytes += size
		logChunkTransfer("team", b.Project, "down", len(batch), len(need), len(fetch), size, started)
		for _, f := range batch {
			// Its version moved on while this page was read (review 5 Nov #8): the newer version comes in a later page.
			if slices.ContainsFunc(f.Version.Chunks, func(ch transferChunk) bool { return moved[ch.ID] }) {
				log.Printf("[CREW_TEAM_LOCAL] project=%s client=%s path=%q event=moved_on", b.Project, b.Client, f.Path)
				continue
			}
			// Edited here while the chunks arrived: keep the local edit; it goes up next round.
			if !unchanged(f.Path) {
				if !b.Downloaded {
					b.Files[f.Path] = crewTeamLocalFile{Version: *f.Version}
					continue
				}
				skipped = true
				continue
			}
			reader, writer := io.Pipe()
			go func(v chunkedFile) {
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
			writeStarted := time.Now()
			err = writeCrewTeamFile(ctx, b.Root, f.Path, reader, *syncFile(*f.Version), op)
			reader.Close()
			if err != nil {
				return crewTeamStep("write", err)
			}
			info, statErr := os.Lstat(filepath.Join(b.Root, filepath.FromSlash(f.Path)))
			if statErr != nil {
				return crewTeamStep("write", statErr)
			}
			b.Files[f.Path] = crewTeamLocalFile{Stamp: crewsync.StampOf(info), ReadAt: writeStarted, Version: *f.Version}
			log.Printf("[CREW_TEAM_LOCAL] project=%s actor=%s client=%s path=%q event=received", b.Project, b.RemoteUser, b.Client, f.Path)
		}
		return nil
	}
	var err error
	for start := 0; start < len(writes) && err == nil; start += crewTeamDownloadBatch {
		end := min(start+crewTeamDownloadBatch, len(writes))
		if err = receive(writes[start:end]); err == nil {
			err = saveCrewTeamLocal(dir, *b)
		}
		if err == nil {
			log.Printf("[CREW_TEAM_LOCAL] project=%s client=%s event=download_progress files=%d/%d bytes=%d/%d", b.Project, b.Client, end, len(writes), doneBytes, total)
		}
	}
	endCrewTeamRound(dir, err)
	return skipped, err
}

// crewTeamChangeSetID names one batch of changes from one copy at the revision it had received: the same batch retried
// has the same id; the same edit made again later is sent from a later revision, so it is a new transfer.
func crewTeamChangeSetID(client string, revision uint64, batch []crewTeamChange) string {
	var key strings.Builder
	key.WriteString(fmt.Sprintf("%s\x00%d", client, revision))
	for _, ch := range batch {
		after := ""
		if ch.After != nil {
			after = fmt.Sprintf("%s/%t", ch.After.Hash, ch.After.Exec)
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
		{"Share folders instead", "what the team shares is over the Team's limit; share folders instead"},
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
