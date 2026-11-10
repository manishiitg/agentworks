package server

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/coding-agent-loop/workspace/crewsync"
	"github.com/manishiitg/coding-agent-loop/workspace/publication"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
	"golang.org/x/text/unicode/norm"
)

type crewTeamVersion struct {
	File       *crewsync.File  `json:"file,omitempty"` // nil is a tombstone
	Chunks     []transferChunk `json:"chunks,omitempty"`
	Revision   uint64          `json:"revision"`
	Author     string          `json:"author"`
	ModifiedAt string          `json:"modified_at"`
}
type crewTeamFileState struct {
	HistoryFloor uint64                     `json:"-"`
	Revision     uint64                     `json:"revision"`
	Versions     map[string]crewTeamVersion `json:"versions"`
	Events       []crewTeamEvent            `json:"events"`
	Clashes      []crewTeamClash            `json:"clashes"`
	Receipts     map[string]crewTeamReceipt `json:"receipts"`
	// OnPurpose: files that look like they hold passwords or keys which a member chose to sync anyway (owner, 5 Nov),
	// by path, with who chose it. Everyone then syncs them like any shared file.
	OnPurpose map[string]string `json:"on_purpose,omitempty"`
}

// crewTeamOnPurpose is a set of exact paths synced on purpose; such a path is shared despite its credential-looking
// name, still never machine state.
type crewTeamOnPurpose map[string]bool

func (s crewTeamFileState) onPurpose() crewTeamOnPurpose {
	op := crewTeamOnPurpose{}
	for p := range s.OnPurpose {
		op[p] = true
	}
	return op
}

// with adds the given exact paths.
func (op crewTeamOnPurpose) with(paths []string) crewTeamOnPurpose {
	out := crewTeamOnPurpose{}
	for p := range op {
		out[p] = true
	}
	for _, p := range paths {
		out[p] = true
	}
	return out
}

func joinOnPurpose(ops []crewTeamOnPurpose) crewTeamOnPurpose {
	if len(ops) == 0 {
		return nil
	}
	return ops[0]
}

type crewTeamReceipt struct {
	TransferID string   `json:"transfer_id"`
	Digest     string   `json:"digest"`
	Revision   uint64   `json:"revision"`
	Conflicts  []string `json:"conflicts"`
}

func crewTeamAllowed(rel string, on ...crewTeamOnPurpose) bool {
	_, err := publication.CleanPath(rel)
	if err != nil || norm.NFC.String(rel) != rel || strings.HasPrefix(rel, "_system/") || rel == "_system" || !crewsync.Allowed("resources/"+rel, false) {
		return false
	}
	if joinOnPurpose(on)[rel] {
		return !publication.ExcludedFromSync(rel, false) // chosen on purpose: credentials allowed, machine state never
	}
	return !publication.Excluded(rel) && !publication.CredentialPath(rel)
}
func crewTeamKey(rel string) string { return "resources/" + rel }
func crewTeamRelative(key string, on ...crewTeamOnPurpose) (string, error) {
	rel, ok := strings.CutPrefix(key, "resources/")
	if !ok || !crewTeamAllowed(rel, on...) {
		return "", fmt.Errorf("only ordinary files inside the shared folder can be transferred")
	}
	return rel, nil
}
func crewTeamRoots(root string) crewroot.Roots { return crewroot.Roots{Files: root, Resources: root} }
func crewTeamFilesRoot(project string) (string, error) {
	d, e := crewTeamContentDir(project)
	if e != nil {
		return "", e
	}
	return filepath.Join(d, "files"), nil
}

func (s crewTeamFileState) manifest() crewsync.Manifest {
	m := crewsync.Manifest{}
	for rel, v := range s.Versions {
		if v.File != nil {
			m[crewTeamKey(rel)] = *v.File
		}
	}
	return m
}

// held is the files the Team shares now (folder): a folder no longer shared keeps its files in the store, unseen, so
// sharing it again needs no upload (announceCrewTeamSharedAgain).
func (s crewTeamFileState) held(folder crewTeamFolder) map[string]crewTeamVersion {
	out := map[string]crewTeamVersion{}
	for rel, v := range s.Versions {
		if v.File != nil && folder.holds(rel) {
			out[rel] = v
		}
	}
	return out
}

// announceCrewTeamSharedAgain records, at a new revision, every file the store already has under folders the Team
// shares now but did not before, so every copy receives them (a copy that already has a file adopts it without a
// write). The caller holds the team lock and has saved the new Team record.
func announceCrewTeamSharedAgain(project, actor string, previous, current crewTeamFolder) error {
	state, err := readCrewTeamFileStateLocked(project)
	if err != nil {
		return err
	}
	paths := []string{}
	for rel := range state.Versions {
		if current.holds(rel) && !previous.holds(rel) {
			paths = append(paths, rel)
		}
	}
	if len(paths) == 0 {
		return nil
	}
	sort.Strings(paths)
	revision, now := state.Revision+1, time.Now().UTC().Format(time.RFC3339Nano)
	for _, rel := range paths {
		v := state.Versions[rel]
		state.Events = append(state.Events, crewTeamEvent{Path: rel, Kind: "shared", Revision: revision, Actor: actor, Author: v.Author, At: now, File: v.File, Chunks: v.Chunks})
	}
	state.Revision = revision
	if err = saveCrewTeamFileState(project, state); err != nil {
		return err
	}
	log.Printf("[CREW_TEAM] project=%s actor=%s event=shared_again files=%d revision=%d", project, logUsernameForUserID(actor), len(paths), revision)
	publishCrewPanelChanged(project)
	return nil
}

func validCrewTeamHash(value string) bool {
	b, e := hex.DecodeString(value)
	return e == nil && len(b) == 32 && value == strings.ToLower(value)
}
func crewTeamSame(a, b *crewsync.File) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
func validateCrewTeamSnapshot(s *crewsync.Snapshot, on ...crewTeamOnPurpose) error {
	if s == nil || s.Git != nil || s.StatePath != "" || s.DatabasePath != "" || len(s.Files) > crewTeamMaxFiles || len(s.Changes) > crewTeamMaxFiles {
		return fmt.Errorf("send only the shared folder's files")
	}
	for name := range s.Payloads {
		if name != "content" {
			return fmt.Errorf("team folders cannot receive settings, credentials or chat history")
		}
	}
	var total int64
	folded := map[string]string{}
	for key, f := range s.Files {
		rel, e := crewTeamRelative(key, on...)
		if e != nil {
			return e
		}
		if f.Size < 0 || f.Size > publication.MaxFileBytes || !validCrewTeamHash(f.Hash) {
			return fmt.Errorf("invalid shared file metadata")
		}
		total += f.Size
		for parent := path.Dir(key); parent != "."; parent = path.Dir(parent) {
			if _, exists := s.Files[parent]; exists {
				return fmt.Errorf("a shared path cannot be both a file and a folder: %s", parent)
			}
		}
		for part := rel; part != "."; part = path.Dir(part) {
			lower := strings.ToLower(part)
			if old, exists := folded[lower]; exists && old != part {
				return fmt.Errorf("file names differ only by letter case")
			}
			folded[lower] = part
		}
	}
	if line := crewTeamTooBigLine("What the team shares", int64(len(s.Files)), total); line != "" {
		return fmt.Errorf("%s", line)
	}
	seen := map[string]bool{}
	for _, c := range s.Changes {
		if _, e := crewTeamRelative(c.Path, on...); e != nil {
			return e
		}
		if seen[c.Path] {
			return fmt.Errorf("duplicate shared file change")
		}
		seen[c.Path] = true
		if c.After != nil {
			if v, ok := s.Files[c.Path]; !ok || v != *c.After {
				return fmt.Errorf("shared file does not match its manifest")
			}
		} else if _, ok := s.Files[c.Path]; ok {
			return fmt.Errorf("deleted file remains in manifest")
		}
		if c.Before != nil && !validCrewTeamHash(c.Before.Hash) {
			return fmt.Errorf("invalid shared file baseline")
		}
	}
	for _, p := range s.Omitted {
		if _, e := crewTeamRelative(p, on...); e != nil {
			return e
		}
	}
	return nil
}

// crewTeamChange is one file of a commit: its path, the version hash the sender started from ("" = it had none) and its
// new version (nil deletes it).
type crewTeamChange struct {
	Path   string       `json:"path"`
	Before string       `json:"before,omitempty"`
	After  *chunkedFile `json:"after,omitempty"`
}

// commitCrewTeamChanges records a commit: each changed file's new version (its chunks must already be in the store),
// a clash when the file moved on since the sender's base (the newer one wins, the other stays in history: keep both),
// the canonical bytes, events and a receipt (a retry with the same transfer returns it). The caller holds the team
// lock and has rechecked current membership. declared are the paths the sender chose to sync on purpose.
func commitCrewTeamChanges(ctx context.Context, project, actor, transfer, digest string, changes []crewTeamChange, declared ...string) (crewTeamReceipt, error) {
	return commitCrewTeamChangesAtRevision(ctx, project, actor, transfer, digest, nil, changes, declared...)
}

func commitCrewTeamChangesAtRevision(ctx context.Context, project, actor, transfer, digest string, cursor *uint64, changes []crewTeamChange, declared ...string) (crewTeamReceipt, error) {
	dir, e := crewTeamContentDir(project)
	if e != nil {
		return crewTeamReceipt{}, e
	}
	state, e := readCrewTeamFileStateLocked(project)
	if e != nil {
		return crewTeamReceipt{}, e
	}
	receiptKey := crewTransferDigest(actor + "\x00" + transfer)
	if old, ok := state.Receipts[receiptKey]; ok {
		if old.Digest != digest {
			return old, fmt.Errorf("this transfer ID was already used for different changes")
		}
		return old, nil
	}
	if cursor != nil && (*cursor < state.HistoryFloor || *cursor > state.Revision) {
		return crewTeamReceipt{}, errCrewTeamHistoryCatchUp
	}
	if transfer == "" || len(transfer) > 257 || !validCrewTeamHash(digest) || len(changes) > crewTeamCommitBatch {
		return crewTeamReceipt{}, fmt.Errorf("use a stable transfer identity and at most %d files", crewTeamCommitBatch)
	}
	team, e := readCrewTeam(project)
	if e != nil {
		return crewTeamReceipt{}, e
	}
	store, e := crewTeamChunkStore(project)
	if e != nil {
		return crewTeamReceipt{}, e
	}
	op := state.onPurpose().with(declared)
	// The limits apply to what the Team shares now; files of a folder no longer shared stay in the store unseen.
	next := crewsync.Manifest{}
	for rel, v := range state.held(team.Folder) {
		next[crewTeamKey(rel)] = *v.File
	}
	seen := map[string]bool{}
	for _, c := range changes {
		if seen[c.Path] || !crewTeamAllowed(c.Path, op) || c.Before != "" && !validCrewTeamHash(c.Before) {
			return crewTeamReceipt{}, fmt.Errorf("only ordinary files inside the shared folder can be changed: %s", c.Path)
		}
		seen[c.Path] = true
		if c.After == nil {
			delete(next, crewTeamKey(c.Path))
			continue
		}
		if !team.Folder.holds(c.Path) {
			return crewTeamReceipt{}, fmt.Errorf("%s is outside what the team shares", c.Path)
		}
		if !c.After.Valid() {
			return crewTeamReceipt{}, fmt.Errorf("invalid shared file metadata: %s", c.Path)
		}
		for _, chunk := range c.After.Chunks {
			if !hasChunk(store, chunk) {
				return crewTeamReceipt{}, fmt.Errorf("send the file's chunks before committing it: %s", c.Path)
			}
		}
		next[crewTeamKey(c.Path)] = *syncFile(*c.After)
	}
	// The whole next folder stays within the limits, with no file/folder or letter-case collisions.
	if e = validateCrewTeamSnapshot(&crewsync.Snapshot{Files: next}, op); e != nil {
		return crewTeamReceipt{}, e
	}
	root := filepath.Join(dir, "files")
	if e = os.MkdirAll(root, 0700); e != nil {
		return crewTeamReceipt{}, e
	}
	receipt := crewTeamReceipt{TransferID: transfer, Digest: digest, Revision: state.Revision + 1, Conflicts: []string{}}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	// A write that fails part way keeps the record in step with the files already written (review 5 Nov #10): what
	// went in is saved as this revision without a receipt, so the retry applies only the rest.
	applied := 0
	notices := []crewTeamClashNotice{}
	partial := func(e error) (crewTeamReceipt, error) {
		if applied > 0 {
			state.Revision = receipt.Revision
			if saveErr := saveCrewTeamFileState(project, state); saveErr != nil {
				log.Printf("[CREW_TEAM] project=%s event=partial_commit_unsaved reason=%q", project, saveErr)
			}
		}
		return receipt, e
	}
	for _, c := range crewTeamOrderedChanges(changes) {
		current := state.Versions[c.Path]
		var after *crewsync.File
		var chunks []transferChunk
		if c.After != nil {
			after, chunks = syncFile(*c.After), c.After.Chunks
		}
		// A file synced on purpose is recorded with who chose it; removing it ends that.
		if after == nil {
			if _, was := state.OnPurpose[c.Path]; was {
				delete(state.OnPurpose, c.Path)
				log.Printf("[CREW_TEAM] project=%s actor=%s event=sync_anyway path=%q on=false", project, actor, c.Path)
			}
		} else if op[c.Path] && !crewTeamAllowed(c.Path) && state.OnPurpose[c.Path] == "" {
			if state.OnPurpose == nil {
				state.OnPurpose = map[string]string{}
			}
			state.OnPurpose[c.Path] = actor
			log.Printf("[CREW_TEAM] project=%s actor=%s event=sync_anyway path=%q on=true", project, actor, c.Path)
		}
		if crewTeamSame(current.File, after) {
			continue
		}
		kind := "sent"
		if after == nil {
			kind = "removed"
		}
		currentHash := ""
		if current.File != nil {
			currentHash = current.File.Hash
		}
		// A clash needs two people and two contents (6 Nov: 7,571 false clashes after a folder change). The same
		// bytes the store already has, the sender's own newer version (another of their copies: the later wins), or
		// a file the store never had are plain changes.
		sameContent := after != nil && currentHash == after.Hash
		if currentHash != c.Before && !sameContent && current.Author != actor && current.Revision != 0 {
			kind = "clash"
		}
		if kind == "clash" && after == nil {
			// A stale delete never beats a teammate's newer edit: the edit stays, and it is recorded again at this
			// revision so every copy, the deleter's included, receives it on its next round.
			state.Clashes = append(state.Clashes, crewTeamClash{Path: c.Path, WinningRevision: receipt.Revision, LosingRevision: current.Revision, WinningAuthor: current.Author, LosingAuthor: actor})
			notices = append(notices, crewTeamClashNotice{clash: state.Clashes[len(state.Clashes)-1], removed: true})
			receipt.Conflicts = append(receipt.Conflicts, c.Path)
			state.Events = append(state.Events, crewTeamEvent{Path: c.Path, Kind: "kept", Revision: receipt.Revision, Actor: actor, Author: current.Author, At: now, File: current.File, Chunks: current.Chunks})
			applied++
			continue
		}
		// Two people editing the same text file from one base: merge by lines (owner, 5 Nov). The merged file is a new
		// revision, so every copy, the sender's included, downloads it next round; only overlapping lines are a clash.
		merged, hunks := false, 0
		var source chunkedFile
		if c.After != nil {
			source = *c.After
		}
		if kind == "clash" && after != nil {
			if v, conflicts, ok := crewTeamMergeVersion(store, state, c.Path, c.Before, current, *c.After); ok {
				source, after, chunks, hunks, merged = v, syncFile(v), v.Chunks, conflicts, true
				kind = "merged"
				result := "merged"
				if hunks > 0 {
					result = "merged_with_clash"
				}
				log.Printf("[CREW_TEAM_SYNC] merge project=%s actor=%s path=%q result=%s hunks=%d", project, actor, c.Path, result, hunks)
			}
		}
		if after == nil {
			if e = removeCrewTeamFile(root, c.Path); e != nil {
				return partial(e)
			}
		} else {
			reader, writer := io.Pipe()
			go func(v chunkedFile) { writer.CloseWithError(assembleChunked(store, v, writer)) }(source)
			e = writeCrewTeamFile(ctx, root, c.Path, reader, *after, op)
			reader.Close()
			if e != nil {
				return partial(e)
			}
		}
		if kind == "clash" || merged && hunks > 0 {
			state.Clashes = append(state.Clashes, crewTeamClash{Path: c.Path, WinningRevision: receipt.Revision, LosingRevision: current.Revision, WinningAuthor: actor, LosingAuthor: current.Author, Merged: merged, Hunks: hunks})
			notices = append(notices, crewTeamClashNotice{clash: state.Clashes[len(state.Clashes)-1]})
			receipt.Conflicts = append(receipt.Conflicts, c.Path)
		}
		state.Events = append(state.Events, crewTeamEvent{Path: c.Path, Kind: kind, Revision: receipt.Revision, Actor: actor, At: now, File: after, Chunks: chunks})
		state.Versions[c.Path] = crewTeamVersion{File: after, Chunks: chunks, Revision: receipt.Revision, Author: actor, ModifiedAt: now}
		applied++
	}
	state.Revision = receipt.Revision
	state.Receipts[receiptKey] = receipt
	if e = saveCrewTeamFileState(project, state); e != nil {
		return receipt, e
	}
	for _, event := range state.Events {
		if event.Revision == state.Revision && len(changes) <= 50 {
			log.Printf("[CREW_TEAM_EVENT] project=%s actor=%s path=%q kind=%s revision=%d", project, event.Actor, event.Path, event.Kind, event.Revision)
		}
	}
	log.Printf("[CREW_TEAM_FILES] project=%s actor=%s transfer=%s revision=%d changes=%d conflicts=%d", project, actor, transfer, state.Revision, len(changes), len(receipt.Conflicts))
	publishCrewPanelChanged(project)
	round, _, _ := strings.Cut(transfer, "/") // the sending copy: one notice per person per round
	notifyCrewTeamClashes(project, round, team, notices)
	return receipt, nil
}

func openCrewTeamFile(root, rel string, on ...crewTeamOnPurpose) (*os.File, error) {
	if !crewTeamAllowed(rel, on...) {
		return nil, fmt.Errorf("this path is outside shared content")
	}
	dir, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	for p := rel; p != "."; p = path.Dir(p) {
		info, e := dir.Lstat(p)
		if e != nil {
			return nil, e
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("links are not shared folder files")
		}
	}
	f, err := dir.Open(rel)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err == nil && info.IsDir() {
		f.Close()
		return nil, &os.PathError{Op: "open", Path: rel, Err: syscall.EISDIR}
	}
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, fmt.Errorf("choose an ordinary file in the shared folder")
	}
	linked, err := publication.MultipleLinks(f, info)
	if err != nil || linked {
		f.Close()
		return nil, fmt.Errorf("hardlinked files are not shared")
	}
	return f, nil
}
func writeCrewTeamFile(ctx context.Context, root, rel string, src io.Reader, meta crewsync.File, on ...crewTeamOnPurpose) error {
	if !crewTeamAllowed(rel, on...) {
		return fmt.Errorf("this path is outside shared content")
	}
	dir, e := os.OpenRoot(root)
	if e != nil {
		return e
	}
	defer dir.Close()
	for p := rel; p != "."; p = path.Dir(p) {
		info, e := dir.Lstat(p)
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return e
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("links are not shared folder files")
		}
	}
	// A copy an agent works in (its folder has the slot group's setgid) gets the group's access, as the slot's own files
	// have: its shell runs as that group. Any other folder, the Team's own store included, stays owner-only.
	group := crewTeamGroupPolicy(root)
	var made []string
	if group {
		for p := path.Dir(rel); p != "."; p = path.Dir(p) {
			if _, statErr := dir.Lstat(p); !os.IsNotExist(statErr) {
				break
			}
			made = append(made, p)
		}
	}
	if e = dir.MkdirAll(path.Dir(rel), 0700); e != nil {
		return e
	}
	for _, p := range made {
		if e = dir.Chmod(p, os.ModeSetgid|0o770); e != nil {
			return e
		}
	}
	tmp := path.Join(path.Dir(rel), ".team-write-"+uuid.NewString())
	f, e := dir.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	defer dir.Remove(tmp)
	// No fsync per file: a 7,184-file apply took 267 s with one each (Showcase, 5 Nov), holding the team lock. The
	// revision is in history before any file is written, and the rename keeps each file whole.
	e = crewsync.CopyChecked(ctx, f, src, meta)
	if e == nil && group {
		e = f.Chmod(map[bool]os.FileMode{true: 0o770, false: 0o660}[meta.Executable])
	} else if e == nil && meta.Executable {
		e = f.Chmod(0700)
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		return e
	}
	return dir.Rename(tmp, rel)
}

func (api *StreamingAPI) handleCrewTeamFiles(w http.ResponseWriter, r *http.Request) {
	project := mux.Vars(r)["project"]
	claims := GetUserFromContext(r.Context())
	rel := r.URL.Query().Get("path")
	write := r.Method != http.MethodGet
	if rel == "" {
		rel = "."
	}
	t, e := crewTeamRequestAuthorize(r, claims, project, rel, write, true)
	if e != nil {
		externalError(w, 403, "team_folder_forbidden", e.Error())
		return
	}
	dir, _ := crewTeamContentDir(project)
	if r.Method == http.MethodGet && mux.Vars(r)["op"] == "files" {
		s, e := readCrewTeamCurrentState(project)
		if e != nil {
			externalError(w, 409, "team_folder_unavailable", e.Error())
			return
		}
		files := []map[string]any{}
		for name, v := range s.Versions {
			if v.File == nil || !crewTeamAllowed(name, s.onPurpose()) || !t.Folder.holds(name) {
				continue
			}
			if token := claims.AccessToken; token != nil && token.FileGuard != nil && !token.FileGuard.Allows(name, false) {
				continue
			}
			files = append(files, map[string]any{"path": name, "size": v.File.Size, "hash": v.File.Hash, "revision": v.Revision, "author": shareOwnerLabel(v.Author), "modified_at": v.ModifiedAt})
		}
		sort.Slice(files, func(i, j int) bool { return files[i]["path"].(string) < files[j]["path"].(string) })
		externalJSON(w, map[string]any{"project_id": project, "folder": t.Folder, "revision": s.Revision, "folder_access": true, "files": files})
		return
	}
	// A file a member chose to sync on purpose is an ordinary shared file here too.
	known, _ := readCrewTeamCurrentState(project)
	op := known.onPurpose()
	if !crewTeamAllowed(rel, op) || !t.Folder.holds(rel) {
		externalError(w, 403, "team_path_forbidden", "Choose a file inside the shared folder; private state and credentials stay private.")
		return
	}
	if r.Method == http.MethodGet {
		root := filepath.Join(dir, "files")
		_, e := crewroot.ResolveContained(root, rel)
		if e != nil {
			externalError(w, 403, "team_path_forbidden", "That path is not available in the shared folder.")
			return
		}
		f, e := openCrewTeamFile(root, rel, op)
		if e != nil {
			externalError(w, 404, "team_file_missing", "This shared file is not available.")
			return
		}
		defer f.Close()
		info, e := f.Stat()
		if e != nil || !info.Mode().IsRegular() {
			externalError(w, 404, "team_file_missing", "Choose a shared file.")
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", "attachment; filename="+fmt.Sprintf("%q", path.Base(rel)))
		http.ServeContent(w, r, path.Base(rel), info.ModTime(), f)
		return
	}
	base := r.URL.Query().Get("base")
	if base != "" && !validCrewTeamHash(base) {
		externalError(w, 400, "invalid_baseline", "Reload the file before saving it.")
		return
	}
	transfer := r.Header.Get("X-Team-Transfer")
	if transfer == "" {
		transfer = r.URL.Query().Get("transfer")
	}
	if transfer == "" || len(transfer) > 128 {
		externalError(w, 400, "transfer_required", "Send the file's saved edit identity.")
		return
	}
	// A saved or uploaded file goes into the chunk store first, then is committed like any synced change.
	var after *chunkedFile
	if r.Method == http.MethodPut {
		store, err := crewTeamChunkStore(project)
		if err != nil {
			externalError(w, 409, "team_write_failed", err.Error())
			return
		}
		body := http.MaxBytesReader(w, r.Body, publication.MaxFileBytes)
		size := r.ContentLength
		if size < 0 {
			raw, err := io.ReadAll(body)
			if err != nil {
				externalError(w, 400, "invalid_file", "Send a complete file of at most 64 MiB.")
				return
			}
			size, body = int64(len(raw)), io.NopCloser(bytes.NewReader(raw))
		}
		version, err := chunkReader(body, size, false, func(id string, data []byte) error { return putChunk(store, id, data) })
		if err != nil {
			externalError(w, 400, "invalid_file", "Send a complete file of at most 64 MiB.")
			return
		}
		after = &version
	} else if r.Method != http.MethodDelete {
		externalError(w, 405, "invalid_action", "Use open, save, upload or delete for this folder.")
		return
	}
	lockDir, _ := crewTeamDir(project)
	unlock, e := wf.LockWorkspace(r.Context(), lockDir)
	if e != nil {
		externalError(w, 409, "team_write_failed", e.Error())
		return
	}
	defer unlock()
	if _, e = crewTeamRequestAuthorize(r, claims, project, rel, true, true); e != nil {
		externalError(w, 403, "team_folder_forbidden", e.Error())
		return
	}
	wire, _ := json.Marshal(after)
	receipt, e := commitCrewTeamChanges(r.Context(), project, claims.UserID, transfer, crewTransferDigest(rel+"\x00"+base+"\x00"+string(wire)), []crewTeamChange{{Path: rel, Before: base, After: after}})
	if e != nil {
		externalError(w, 409, "team_write_failed", e.Error())
		return
	}
	externalJSON(w, receipt)
}

// A request-scoped identity for a browser upload, also used by chat-tool writes.
func newCrewTeamTransfer() string { return uuid.NewString() }

func crewTeamOrderedChanges(input []crewTeamChange) []crewTeamChange {
	changes := append([]crewTeamChange(nil), input...)
	sort.SliceStable(changes, func(i, j int) bool {
		if (changes[i].After == nil) != (changes[j].After == nil) {
			return changes[i].After == nil
		}
		return changes[i].Path < changes[j].Path
	})
	return changes
}

// Removing a final child also removes empty parents so a directory can become a
// file on the next change. A nonempty parent (including local edits) is retained.
func removeCrewTeamFile(root, rel string) error {
	dir, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer dir.Close()
	if err = dir.Remove(rel); err != nil {
		if os.IsNotExist(err) || errors.Is(err, syscall.ENOTDIR) {
			return nil
		}
		return err
	}
	for parent := path.Dir(rel); parent != "."; parent = path.Dir(parent) {
		if err = dir.Remove(parent); err != nil {
			break
		}
	}
	return nil
}

// crewTeamGroupPolicy: the folder keeps the slot group (setgid), so what is written there must be group-accessible.
func crewTeamGroupPolicy(root string) bool {
	info, err := os.Stat(root)
	return err == nil && info.Mode()&os.ModeSetgid != 0
}

// repairCrewTeamCopyModes gives a copy written before this (owner-only folders and files) the group's access, once:
// folders rwx with setgid, files rw (rwx where the owner can run them). It returns how many entries it changed.
func repairCrewTeamCopyModes(root string) (changed int, err error) {
	if !crewTeamGroupPolicy(root) {
		return 0, nil
	}
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		info, e := d.Info()
		if e != nil || info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		want := info.Mode().Perm()
		switch {
		case d.IsDir():
			if info.Mode()&os.ModeSetgid != 0 && want&0o070 == 0o070 {
				return nil
			}
			if e = os.Chmod(p, os.ModeSetgid|0o770); e == nil {
				changed++
			}
		case info.Mode().IsRegular():
			group := os.FileMode(0o060)
			if want&0o100 != 0 {
				group = 0o070
			}
			if want&group == group {
				return nil
			}
			if e = os.Chmod(p, want|group); e == nil {
				changed++
			}
		}
		return nil
	})
	return changed, err
}
