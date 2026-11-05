package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/filesnapshots"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/coding-agent-loop/workspace/crewsync"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

type crewTeamLocal struct {
	FolderID     string            `json:"folder_id"`
	Connection   string            `json:"connection"`
	Project      string            `json:"project_id"`
	LocalProject string            `json:"local_project_id,omitempty"`
	RemoteUser   string            `json:"remote_user"`
	Client       string            `json:"client"`
	Root         string            `json:"local_folder"`
	LocalBase    crewsync.Manifest `json:"local_base"`
	RemoteBase   crewsync.Manifest `json:"remote_base"`
	LastSynced   string            `json:"last_synced,omitempty"`
	Status       string            `json:"status"`
	Error        string            `json:"error,omitempty"`
	// "Sync it anyway" (owner, 5 Nov): this person's own choice of files that look like they hold passwords or keys,
	// by exact path; StopSyncing are ones taken back, removed from the shared folder on the next upload (the files
	// stay here). FolderOnPurpose is what the shared folder holds on purpose, from the server's last answer.
	SyncAnyway      []string `json:"sync_anyway,omitempty"`
	StopSyncing     []string `json:"stop_syncing,omitempty"`
	FolderOnPurpose []string `json:"folder_on_purpose,omitempty"`
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
	return filepath.Join(root, "team-working", crewTransferDigest(owner), crewTransferDigest(c.Server+"\x00"+c.User.ID+"\x00"+project+"\x00"+folderID)), nil
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
	return fsutil.WriteJSONAtomic(filepath.Join(dir, "binding.json"), b, 0600)
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
	b := crewTeamLocal{FolderID: t.Folder.ID, Connection: c.ID, Project: t.ProjectID, RemoteUser: c.User.ID, Client: uuid.NewString(), LocalBase: crewsync.Manifest{}, RemoteBase: crewsync.Manifest{}, Status: "pending"}
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
						round, done := context.WithTimeout(ctx, 3*time.Minute)
						err = syncCrewTeamLocal(round, owner, c, dir, &b)
						done()
						if err != nil {
							b.Status = "pending"
							b.Error = crewTeamPlainSyncError(err)
							_ = saveCrewTeamLocal(dir, b)
							log.Printf("[CREW_TEAM] project=%s event=sync_failed shown=%q reason=%q", t.ProjectID, b.Error, err)
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
	info, e := os.Lstat(b.Root)
	if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("the shared folder moved; open it in Teams to reconnect it")
	}
	if c.User.ID != b.RemoteUser {
		return fmt.Errorf("this folder belongs to a different saved account")
	}
	// Recheck before capture, in addition to the server's checks on every chunk.
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
	for _, direction := range []string{"upload", "download"} {
		stage := filepath.Join(dir, direction)
		if e = os.MkdirAll(stage, 0700); e != nil {
			return e
		}
		store := filesnapshots.Store{Root: b.Root, Private: stage}
		meta := filepath.Join(stage, "pending.json")
		var pending crewTeamTransfer
		if raw, err := os.ReadFile(meta); err == nil {
			if e = json.Unmarshal(raw, &pending); e != nil {
				return e
			}
		} else if !os.IsNotExist(err) {
			return err
		}
		if pending.TransferID == "" {
			pending = crewTeamTransfer{FolderID: b.FolderID, Client: b.Client, TransferID: uuid.NewString(), Direction: direction}
			if direction == "upload" {
				snapshot, err := crewTeamCapture(ctx, b.Root, stage, b.LocalBase, team.Folder, b.onPurpose())
				if err != nil {
					return err
				}
				// Stop syncing removes the file from the shared folder; it stays on this computer.
				for _, rel := range b.StopSyncing {
					key := crewTeamKey(rel)
					before, ok := b.LocalBase[key]
					if !ok || slices.ContainsFunc(snapshot.Changes, func(c crewsync.Change) bool { return c.Path == key }) {
						continue
					}
					delete(snapshot.Files, key)
					snapshot.Changes = append(snapshot.Changes, crewsync.Change{Path: key, Before: &before})
				}
				if len(snapshot.Changes) == 0 {
					snapshot.Close()
					continue
				}
				err = store.EncodeFolderSync(ctx, crewTeamPair(b.Project, b.RemoteUser, b.Client, direction), crewTeamRoots(b.Root), &snapshot)
				if err == nil {
					err = copyCrewTeamBundle(snapshot.Content.BundlePath, filepath.Join(stage, "content"))
					_ = os.RemoveAll(filepath.Dir(snapshot.Content.BundlePath))
				}
				snapshot.Close()
				if err != nil {
					return err
				}
				payload, err := crewTeamPayload(filepath.Join(stage, "content"))
				if err != nil {
					return err
				}
				snapshot.Payloads = map[string]crewsync.Payload{"content": payload}
				pending.Snapshot = &snapshot
				pending.OnPurpose = slices.Clone(b.SyncAnyway)
			} else {
				pending.Action = "pull"
				pending.Base = b.RemoteBase
				if e = crewTeamRemoteJSON(ctx, owner, c, http.MethodPost, "/api/crew-teams/"+url.PathEscape(b.Project)+"/sync?folder_id="+url.QueryEscape(b.FolderID), pending, &pending); e != nil {
					return e
				}
				b.FolderOnPurpose = pending.OnPurpose // the server names what the folder holds on purpose
			}
			if pending.Unchanged {
				continue
			}
			if e = fsutil.WriteJSONAtomic(meta, pending, 0600); e != nil {
				return e
			}
		}
		if pending.Snapshot == nil {
			return fmt.Errorf("saved folder transfer is incomplete")
		}
		snapshot := pending.Snapshot
		// The round's numbers for the progress bar: files in this round and the bundle's bytes (crew_team_progress.go).
		roundCtx := startCrewTeamRound(ctx, dir, b.Project, direction, len(snapshot.Changes), snapshot.Payloads["content"].Size, crewTeamStagedBytes(filepath.Join(stage, "content"), direction))
		e = func(ctx context.Context) error {
			pair := crewTeamPair(b.Project, b.RemoteUser, b.Client, direction)
			route := "/api/crew-teams/" + url.PathEscape(b.Project) + "/sync?folder_id=" + url.QueryEscape(b.FolderID)
			if direction == "upload" {
				pending.Action = "prepare"
				if e = crewTeamRemoteJSON(ctx, owner, c, http.MethodPost, route, pending, nil); e != nil {
					return e
				}
				if e = transferCrewTeamBundle(ctx, c, b.Project, pending, filepath.Join(stage, "content")); e != nil {
					return e
				}
				pending.Action = "apply"
				if e = crewTeamRemoteJSON(ctx, owner, c, http.MethodPost, route, pending, nil); e != nil {
					return e
				}
				if e = store.AckSync(ctx, pair, snapshot.Content); e != nil {
					return e
				}
				b.LocalBase = snapshot.Files
				// Sent: the folder now holds this copy's choices and no longer the ones it took back.
				folder := crewTeamOnPurpose{}.with(b.FolderOnPurpose).with(pending.OnPurpose)
				for _, p := range b.StopSyncing {
					if _, kept := snapshot.Files[crewTeamKey(p)]; !kept {
						delete(folder, p)
					}
				}
				b.FolderOnPurpose, b.StopSyncing = sortedOnPurpose(folder), nil
			} else {
				if e = transferCrewTeamBundle(ctx, c, b.Project, pending, filepath.Join(stage, "content")); e != nil {
					return e
				}
				snapshot.Content.BundlePath = filepath.Join(stage, "content")
				if e = store.DecodeSync(ctx, pair, snapshot); e != nil {
					return e
				}
				e = applyCrewTeamDownload(ctx, b, snapshot)
				snapshot.Close()
				if e != nil {
					return e
				}
				if e = store.AckSync(ctx, pair, snapshot.Content); e != nil {
					return e
				}
				b.RemoteBase = snapshot.Files
				// Save applied paths before acknowledging so retries can distinguish
				// an arriving server file from an edit made here during the download.
				if e = saveCrewTeamLocal(dir, *b); e != nil {
					return e
				}
				pending.Action = "ack"
				if e = crewTeamRemoteJSON(ctx, owner, c, http.MethodPost, route, pending, nil); e != nil {
					return e
				}
			}
			if e = saveCrewTeamLocal(dir, *b); e != nil {
				return e
			}
			if e = os.Remove(meta); e != nil {
				return e
			}
			return nil
		}(roundCtx)
		endCrewTeamRound(dir, e)
		if e != nil {
			return e
		}
		_ = os.Remove(filepath.Join(stage, "content"))
	}
	check, e := crewTeamCapture(ctx, b.Root, dir, b.LocalBase, team.Folder, b.onPurpose())
	if e != nil {
		return e
	}
	defer check.Close()
	b.Status = "up_to_date"
	if len(check.Changes) > 0 {
		b.Status = "pending"
	}
	b.Error = ""
	b.LastSynced = time.Now().UTC().Format(time.RFC3339Nano)
	log.Printf("[CREW_TEAM_LOCAL] project=%s client=%s event=reconciled status=%s", b.Project, b.Client, b.Status)
	return saveCrewTeamLocal(dir, *b)
}

func crewTeamCurrentFile(root, rel string, on ...crewTeamOnPurpose) (*crewsync.File, error) {
	f, e := openCrewTeamFile(root, rel, on...)
	if os.IsNotExist(e) || errors.Is(e, syscall.ENOTDIR) || errors.Is(e, syscall.EISDIR) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil {
		return nil, e
	}
	h := sha256.New()
	n, e := io.Copy(h, f)
	if e != nil {
		return nil, e
	}
	return &crewsync.File{Hash: hex.EncodeToString(h.Sum(nil)), Size: n, Executable: info.Mode()&0111 != 0}, nil
}
func applyCrewTeamDownload(ctx context.Context, b *crewTeamLocal, s *crewsync.Snapshot) error {
	op := b.onPurpose()
	if e := validateCrewTeamSnapshot(s, op); e != nil {
		return e
	}
	if b.LocalBase == nil {
		b.LocalBase = crewsync.Manifest{}
	}
	for _, change := range crewTeamOrderedChanges(s.Changes) {
		rel, _ := crewTeamRelative(change.Path, op)
		actual, e := crewTeamCurrentFile(b.Root, rel, op)
		if e != nil {
			return e
		}
		var base *crewsync.File
		if known, ok := b.LocalBase[change.Path]; ok {
			base = &known
		}
		if !crewTeamSame(actual, change.After) {
			if !crewTeamSame(actual, base) {
				continue
			} // changed here in flight: upload it next round
			if change.After == nil {
				if e = removeCrewTeamFile(b.Root, rel); e != nil {
					return e
				}
			} else {
				f, e := os.Open(change.DataPath)
				if e != nil {
					return e
				}
				e = writeCrewTeamFile(ctx, b.Root, rel, f, *change.After, op)
				f.Close()
				if e != nil {
					return e
				}
			}
		}
		log.Printf("[CREW_TEAM_LOCAL] project=%s actor=%s client=%s path=%q event=received deleted=%t", b.Project, b.RemoteUser, b.Client, rel, change.After == nil)
		if change.After == nil {
			delete(b.LocalBase, change.Path)
		} else {
			b.LocalBase[change.Path] = *change.After
		}
	}
	return nil
}

func transferCrewTeamBundle(ctx context.Context, c desktopConnection, project string, t crewTeamTransfer, file string) error {
	p := t.Snapshot.Payloads["content"]
	if !crewPayloadValid(p) {
		return fmt.Errorf("invalid file bundle")
	}
	request := func(method string, offset int64, body io.Reader, n int64) (*http.Response, error) {
		q := url.Values{"folder_id": {t.FolderID}, "client": {t.Client}, "direction": {t.Direction}, "offset": {strconv.FormatInt(offset, 10)}}
		r, e := http.NewRequestWithContext(ctx, method, c.Server+"/api/crew-teams/"+url.PathEscape(project)+"/sync/"+url.PathEscape(t.TransferID)+"?"+q.Encode(), body)
		if e != nil {
			return nil, e
		}
		r.Header.Set("Authorization", "Bearer "+c.Token)
		r.ContentLength = n
		if method == http.MethodGet && offset > 0 {
			r.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
		}
		client := crewTeamHTTPClient(ctx)
		client.Timeout = 2 * time.Minute
		res, e := client.Do(r)
		if e != nil {
			return nil, e
		}
		if res.StatusCode != 200 && res.StatusCode != 204 && res.StatusCode != 206 {
			res.Body.Close()
			return nil, fmt.Errorf("shared folder transfer refused (%d); check your current Team access", res.StatusCode)
		}
		return res, nil
	}
	if t.Direction == "upload" {
		if e := crewCheckPayloadFile(file, p); e != nil {
			return e
		}
		res, e := request(http.MethodHead, 0, nil, 0)
		if e != nil {
			return e
		}
		res.Body.Close()
		offset, e := strconv.ParseInt(res.Header.Get("Upload-Offset"), 10, 64)
		if e != nil || offset < 0 || offset > p.Size || res.Header.Get("X-Sync-Hash") != p.Hash {
			return fmt.Errorf("invalid saved transfer offset")
		}
		f, e := os.Open(file)
		if e != nil {
			return e
		}
		defer f.Close()
		for offset < p.Size {
			n := min(crewSyncPieceBytes, p.Size-offset)
			res, e = request(http.MethodPut, offset, io.NewSectionReader(f, offset, n), n)
			if e != nil {
				return e
			}
			res.Body.Close()
			next, e := strconv.ParseInt(res.Header.Get("Upload-Offset"), 10, 64)
			if e != nil || next != offset+n {
				return fmt.Errorf("upload acknowledgement is incomplete")
			}
			offset = next
			crewTeamRoundBytes(ctx, offset)
		}
		return nil
	}
	offset := int64(0)
	if info, e := os.Stat(file); e == nil {
		offset = info.Size()
	}
	if offset > p.Size {
		return fmt.Errorf("saved download is larger than its snapshot")
	}
	if offset < p.Size {
		res, e := request(http.MethodGet, offset, nil, 0)
		if e != nil {
			return e
		}
		defer res.Body.Close()
		if offset > 0 && res.StatusCode != 206 {
			return fmt.Errorf("server did not resume the folder download")
		}
		if res.Header.Get("X-Sync-Hash") != p.Hash {
			return fmt.Errorf("download does not match its saved snapshot")
		}
		f, e := os.OpenFile(file, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if e != nil {
			return e
		}
		_, e = io.CopyN(f, &crewTeamCountingReader{ctx: ctx, r: res.Body, done: offset}, p.Size-offset)
		ce := f.Close()
		if e == nil {
			e = ce
		}
		if e != nil {
			return e
		}
	}
	if e := crewCheckPayloadFile(file, p); e != nil {
		_ = os.Remove(file)
		return e
	}
	return nil
}

// crewTeamStagedBytes is how much of a download is already on disk when a round resumes (an upload's offset comes from
// the server on its first request).
func crewTeamStagedBytes(file, direction string) int64 {
	if direction != "download" {
		return 0
	}
	if info, err := os.Stat(file); err == nil {
		return info.Size()
	}
	return 0
}

// crewTeamPlainSyncError is what the person sees for a failed sync round ("Can't sync: …", owner 5 Nov): one plain
// sentence, never a raw error, path, address or ID (the raw one goes to the log).
func crewTeamPlainSyncError(err error) string {
	text := err.Error()
	for _, known := range []struct{ match, say string }{
		{"cannot reach your company's server", "your company's server can't be reached"},
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
