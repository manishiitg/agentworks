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
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/filesnapshots"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/coding-agent-loop/workspace/crewsync"
	"github.com/manishiitg/coding-agent-loop/workspace/publication"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
	"golang.org/x/text/unicode/norm"
)

type crewTeamVersion struct {
	File       *crewsync.File `json:"file,omitempty"` // nil is a tombstone
	Revision   uint64         `json:"revision"`
	Author     string         `json:"author"`
	ModifiedAt string         `json:"modified_at"`
}
type crewTeamFileState struct {
	Revision uint64                     `json:"revision"`
	Versions map[string]crewTeamVersion `json:"versions"`
	Events   []crewTeamEvent            `json:"events"`
	Clashes  []crewTeamClash            `json:"clashes"`
	Receipts map[string]crewTeamReceipt `json:"receipts"`
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
func readCrewTeamFileState(project string) (crewTeamFileState, error) {
	s := crewTeamFileState{Versions: map[string]crewTeamVersion{}, Receipts: map[string]crewTeamReceipt{}}
	d, e := crewTeamContentDir(project)
	if e != nil {
		return s, e
	}
	raw, e := os.ReadFile(filepath.Join(d, "files.json"))
	if os.IsNotExist(e) {
		return s, nil
	}
	if e != nil {
		return s, e
	}
	e = json.Unmarshal(raw, &s)
	if s.Versions == nil {
		s.Versions = map[string]crewTeamVersion{}
	}
	if s.Receipts == nil {
		s.Receipts = map[string]crewTeamReceipt{}
	}
	return s, e
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
func crewTeamCapture(ctx context.Context, root, private string, base crewsync.Manifest, on ...crewTeamOnPurpose) (crewsync.Snapshot, error) {
	if e := os.MkdirAll(private, 0700); e != nil {
		return crewsync.Snapshot{}, e
	}
	s, e := crewsync.Capture(ctx, crewTeamRoots(root), base, private)
	if e != nil {
		return s, e
	}
	for key := range s.Files {
		rel, err := crewTeamRelative(key, on...)
		if err != nil {
			delete(s.Files, key)
			continue
		}
		f, err := openCrewTeamFile(root, rel, on...)
		if err != nil {
			s.Close()
			return crewsync.Snapshot{}, err
		}
		f.Close()
	}
	changes := s.Changes[:0]
	for _, c := range s.Changes {
		if _, e := crewTeamRelative(c.Path, on...); e == nil {
			changes = append(changes, c)
		}
	}
	s.Changes = changes
	omitted := s.Omitted[:0]
	for _, p := range s.Omitted {
		if _, e := crewTeamRelative(p, on...); e == nil {
			omitted = append(omitted, p)
		}
	}
	s.Omitted = omitted
	return s, nil
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
	if s == nil || s.Git != nil || s.StatePath != "" || s.DatabasePath != "" || len(s.Files) > publication.MaxFiles || len(s.Changes) > publication.MaxFiles {
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
	if total > publication.MaxBytes {
		return fmt.Errorf("shared folder exceeds 256 MiB")
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

// The caller holds the team lock and has rechecked current membership. Only this
// writer mutates canonical bytes; native agents use their own working copies.
// declared are the paths the uploader chose to sync on purpose in this transfer.
func applyCrewTeamChanges(ctx context.Context, project, actor, transfer, digest string, snapshot crewsync.Snapshot, declared ...string) (crewTeamReceipt, error) {
	dir, e := crewTeamContentDir(project)
	if e != nil {
		return crewTeamReceipt{}, e
	}
	state, e := readCrewTeamFileState(project)
	if e != nil {
		return crewTeamReceipt{}, e
	}
	receiptKey := crewTransferDigest(actor + "\x00" + transfer)
	var receipt crewTeamReceipt
	if old, ok := state.Receipts[receiptKey]; ok {
		if old.Digest != digest {
			return old, fmt.Errorf("this transfer ID was already used for different changes")
		}
		return old, nil
	}
	if transfer == "" || len(transfer) > 257 || !validCrewTeamHash(digest) {
		return receipt, fmt.Errorf("use a stable transfer identity")
	}
	op := state.onPurpose().with(declared)
	if e = validateCrewTeamSnapshot(&snapshot, op); e != nil {
		return receipt, e
	}
	root := filepath.Join(dir, "files")
	if e = os.MkdirAll(root, 0700); e != nil {
		return receipt, e
	}
	// Validate every changed payload before altering any file.
	for _, c := range snapshot.Changes {
		if c.After != nil {
			if e = c.CopyTo(ctx, io.Discard); e != nil {
				return receipt, e
			}
		}
	}
	next := state.manifest()
	for _, c := range snapshot.Changes {
		if c.After == nil {
			delete(next, c.Path)
		} else {
			next[c.Path] = *c.After
		}
	}
	if e = validateCrewTeamSnapshot(&crewsync.Snapshot{Files: next}, op); e != nil {
		return receipt, e
	}
	receipt = crewTeamReceipt{TransferID: transfer, Digest: digest, Revision: state.Revision + 1, Conflicts: []string{}}
	changes := crewTeamOrderedChanges(snapshot.Changes)
	// Save the complete next revision before replacing working bytes. A failed
	// disk write therefore never discards the previous or incoming version.
	history := filesnapshots.Store{Root: root, Private: filepath.Join(dir, "history")}
	if e = history.SaveFolderRevision(ctx, fmt.Sprint(receipt.Revision), fmt.Sprint(state.Revision), crewTeamRoots(root), crewsync.Snapshot{Files: next, Changes: changes}); e != nil {
		return receipt, e
	}
	for _, c := range changes {
		rel, _ := crewTeamRelative(c.Path, op)
		current := state.Versions[rel]
		// A file synced on purpose is recorded with who chose it; removing it ends that.
		if c.After == nil {
			if _, was := state.OnPurpose[rel]; was {
				delete(state.OnPurpose, rel)
				log.Printf("[CREW_TEAM] project=%s actor=%s event=sync_anyway path=%q on=false", project, actor, rel)
			}
		} else if op[rel] && !crewTeamAllowed(rel) && state.OnPurpose[rel] == "" {
			if state.OnPurpose == nil {
				state.OnPurpose = map[string]string{}
			}
			state.OnPurpose[rel] = actor
			log.Printf("[CREW_TEAM] project=%s actor=%s event=sync_anyway path=%q on=true", project, actor, rel)
		}
		if crewTeamSame(current.File, c.After) {
			continue
		}
		kind := "sent"
		if c.After == nil {
			kind = "removed"
		}
		if !crewTeamSame(current.File, c.Before) {
			kind = "clash"
			state.Clashes = append(state.Clashes, crewTeamClash{Path: rel, WinningRevision: receipt.Revision, LosingRevision: current.Revision, WinningAuthor: actor, LosingAuthor: current.Author})
			receipt.Conflicts = append(receipt.Conflicts, rel)
		}
		state.Events = append(state.Events, crewTeamEvent{Path: rel, Kind: kind, Revision: receipt.Revision, Actor: actor, At: time.Now().UTC().Format(time.RFC3339Nano), File: c.After})
		if c.After == nil {
			if e = removeCrewTeamFile(root, rel); e != nil {
				return receipt, e
			}
		} else {
			f, e := os.Open(c.DataPath)
			if e != nil {
				return receipt, e
			}
			e = writeCrewTeamFile(ctx, root, rel, f, *c.After, op)
			f.Close()
			if e != nil {
				return receipt, e
			}
		}
		state.Versions[rel] = crewTeamVersion{File: c.After, Revision: receipt.Revision, Author: actor, ModifiedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	}
	state.Revision = receipt.Revision
	state.Receipts[receiptKey] = receipt
	if e = fsutil.WriteJSONAtomic(filepath.Join(dir, "files.json"), state, 0600); e != nil {
		return receipt, e
	}
	for _, event := range state.Events {
		if event.Revision == state.Revision {
			log.Printf("[CREW_TEAM_EVENT] project=%s actor=%s path=%q kind=%s revision=%d", project, event.Actor, event.Path, event.Kind, event.Revision)
		}
	}
	log.Printf("[CREW_TEAM_FILES] project=%s actor=%s transfer=%s revision=%d changes=%d conflicts=%d", project, actor, transfer, state.Revision, len(changes), len(receipt.Conflicts))
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
	if e = dir.MkdirAll(path.Dir(rel), 0700); e != nil {
		return e
	}
	tmp := path.Join(path.Dir(rel), ".team-write-"+uuid.NewString())
	f, e := dir.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	defer dir.Remove(tmp)
	e = crewsync.CopyChecked(ctx, f, src, meta)
	if e == nil {
		e = f.Sync()
	}
	if e == nil && meta.Executable {
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
		s, e := readCrewTeamFileState(project)
		if e != nil {
			externalError(w, 409, "team_folder_unavailable", e.Error())
			return
		}
		files := []map[string]any{}
		for name, v := range s.Versions {
			if v.File == nil || !crewTeamAllowed(name, s.onPurpose()) {
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
	known, _ := readCrewTeamFileState(project)
	op := known.onPurpose()
	if !crewTeamAllowed(rel, op) {
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
	var after *crewsync.File
	var stage string
	if r.Method == http.MethodPut {
		stageDir := filepath.Join(dir, "incoming")
		if e = os.MkdirAll(stageDir, 0700); e != nil {
			externalError(w, 409, "team_write_failed", e.Error())
			return
		}
		f, e := os.CreateTemp(stageDir, "file-")
		if e != nil {
			externalError(w, 409, "team_write_failed", e.Error())
			return
		}
		stage = f.Name()
		defer os.Remove(stage)
		h := sha256.New()
		n, e := io.Copy(io.MultiWriter(f, h), http.MaxBytesReader(w, r.Body, publication.MaxFileBytes))
		ce := f.Close()
		if e == nil {
			e = ce
		}
		if e != nil {
			externalError(w, 400, "invalid_file", "Send a complete file of at most 64 MiB.")
			return
		}
		after = &crewsync.File{Hash: hex.EncodeToString(h.Sum(nil)), Size: n}
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
	state, e := readCrewTeamFileState(project)
	if e != nil {
		externalError(w, 409, "team_write_failed", e.Error())
		return
	}
	var before *crewsync.File
	if base != "" {
		before = &crewsync.File{Hash: base}
		if current := state.Versions[rel].File; current != nil && current.Hash == base {
			before = current
		}
	}
	files := state.manifest()
	if after == nil {
		delete(files, crewTeamKey(rel))
	} else {
		files[crewTeamKey(rel)] = *after
	}
	change := crewsync.Change{Path: crewTeamKey(rel), Before: before, After: after, DataPath: stage}
	wire, _ := json.Marshal(after)
	receipt, e := applyCrewTeamChanges(r.Context(), project, claims.UserID, transfer, crewTransferDigest(rel+"\x00"+base+"\x00"+string(wire)), crewsync.Snapshot{Files: files, Changes: []crewsync.Change{change}})
	if e != nil {
		externalError(w, 409, "team_write_failed", e.Error())
		return
	}
	externalJSON(w, receipt)
}

// A request-scoped identity for a browser upload, also used by chat-tool writes.
func newCrewTeamTransfer() string { return uuid.NewString() }

func crewTeamOrderedChanges(input []crewsync.Change) []crewsync.Change {
	changes := append([]crewsync.Change(nil), input...)
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
