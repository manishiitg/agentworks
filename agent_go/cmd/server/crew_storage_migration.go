package server

import (
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
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
)

// One administrator operation previews, backs up and moves existing projects.
type crewStorageProject struct {
	Key             string   `json:"key"`
	WorkspaceID     string   `json:"workspace_id,omitempty"`
	Name            string   `json:"name"`
	Owner           string   `json:"owner"`
	OwnerName       string   `json:"owner_name"`
	Origin          string   `json:"origin"`
	Source          string   `json:"source_root"`
	Destination     string   `json:"destination_root"`
	Mode            string   `json:"mode,omitempty"`
	Memory          bool     `json:"memory_enabled"`
	LocalMetadata   bool     `json:"local_metadata_only,omitempty"`
	State           string   `json:"state"`
	Files           int64    `json:"files"`
	Bytes           int64    `json:"bytes"`
	Fingerprint     string   `json:"fingerprint,omitempty"`
	RecordHash      string   `json:"record_hash,omitempty"`
	Reasons         []string `json:"reasons,omitempty"`
	BackupState     string   `json:"backup_state,omitempty"`
	ArchiveSHA      string   `json:"archive_sha256,omitempty"`
	ArchiveBytes    int64    `json:"archive_bytes,omitempty"`
	RestoreVerified bool     `json:"restore_verified"`
}

type crewStorageSnapshot struct {
	ID          string                        `json:"operation_id"`
	Actor       string                        `json:"actor_id"`
	BackupActor string                        `json:"backup_actor_id,omitempty"`
	MoveActor   string                        `json:"move_actor_id,omitempty"`
	CreatedAt   string                        `json:"created_at"`
	State       string                        `json:"state"`
	RegistrySHA string                        `json:"registry_sha256"`
	Projects    []crewStorageProject          `json:"projects"`
	Records     map[string]projectOwnerRecord `json:"records"`
	Error       string                        `json:"error,omitempty"`
}

var crewStorageJobs sync.Map

func crewStorageDir(id string) (string, error) {
	if _, err := uuid.Parse(id); err != nil {
		return "", fmt.Errorf("choose a storage preview first")
	}
	root, err := workflowCLIStateRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "crew-storage", id), nil
}

func crewStorageSave(s *crewStorageSnapshot) error {
	dir, err := crewStorageDir(s.ID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".snapshot-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err = tmp.Write(raw); err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, "snapshot.json"))
}

func crewStorageRead(id string) (*crewStorageSnapshot, error) {
	dir, err := crewStorageDir(id)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(filepath.Join(dir, "snapshot.json"))
	if err != nil {
		return nil, err
	}
	var s crewStorageSnapshot
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, err
	}
	if s.ID != id {
		return nil, fmt.Errorf("storage preview identity does not match")
	}
	if _, active := crewStorageJobs.Load(id); !active && (s.State == "scanning" || s.State == "backing_up" || s.State == "moving") {
		s.State, s.Error = "interrupted", "This check stopped before completing. Start a fresh preview."
	}
	return &s, nil
}

func crewStorageHash(value any) string {
	raw, _ := json.Marshal(value)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func crewStorageFileHash(name string) (string, int64, error) {
	file, err := os.Open(name)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	h := sha256.New()
	n, err := io.Copy(h, file)
	return hex.EncodeToString(h.Sum(nil)), n, err
}

// WalkDir sorts each directory and streams one entry at a time. A private
// manifest can contain a million entries without a million-entry Go slice.
func crewStorageInventory(root string, output io.Writer) (fingerprint string, files, bytes int64, err error) {
	h := sha256.New()
	var device any
	err = filepath.WalkDir(root, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		metadata, err := crewStorageMetadata(name, info)
		if err != nil {
			return fmt.Errorf("capture metadata for %s: %w", rel, err)
		}
		if rel == "." {
			device = metadata["device"]
		} else if metadata["device"] != device {
			return fmt.Errorf("another filesystem is mounted inside this project")
		}
		row := map[string]any{"path": filepath.ToSlash(rel), "metadata": metadata, "mtime_ns": info.ModTime().UnixNano()}
		switch {
		case info.Mode().IsRegular():
			sum, size, err := crewStorageFileHash(name)
			if err != nil {
				return fmt.Errorf("read %s: %w", rel, err)
			}
			row["sha256"], row["bytes"] = sum, size
			files++
			bytes += size
		case info.IsDir():
		case info.Mode()&os.ModeSymlink != 0:
			link, err := os.Readlink(name)
			if err != nil {
				return err
			}
			row["link"] = link
		default:
			return fmt.Errorf("unsupported file type at %s", rel)
		}
		if err := json.NewEncoder(output).Encode(row); err != nil {
			return err
		}
		// Device/inode identity changes on restore; ownership, permissions,
		// links, timestamps, xattrs and file bytes must match exactly.
		delete(metadata, "device")
		if err := json.NewEncoder(h).Encode(row); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return "", files, bytes, err
	}
	return hex.EncodeToString(h.Sum(nil)), files, bytes, nil
}

func crewStorageReadSettings(root string, row *crewStorageProject) error {
	read := func(name string, value any) error {
		target, err := filepath.EvalSymlinks(filepath.Join(root, name))
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, target)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return fmt.Errorf("settings are outside this project")
		}
		f, err := os.Open(target)
		if err != nil {
			return err
		}
		defer f.Close()
		raw, err := io.ReadAll(io.LimitReader(f, (2<<20)+1))
		if err != nil || len(raw) > 2<<20 {
			return fmt.Errorf("settings could not be read completely")
		}
		return json.Unmarshal(raw, value)
	}
	var product struct{ ID, Title, Product, Mode string }
	if err := read("product.json", &product); err != nil {
		return fmt.Errorf("project settings unavailable")
	}
	if product.ID == "" || product.Product != row.Origin || row.WorkspaceID != row.Origin+":"+product.ID {
		return fmt.Errorf("project identity does not match its server record")
	}
	row.Name, row.Mode = product.Title, product.Mode
	if row.Name == "" {
		row.Name = product.ID
	}
	if row.Mode == "" {
		row.Mode = "dev"
	}
	if row.Mode != "dev" && row.Mode != "cowork" && row.Mode != "local" {
		return fmt.Errorf("project mode is not recognized")
	}
	var runtime struct {
		Capabilities struct {
			Memory *bool `json:"memory_enabled"`
		} `json:"capabilities"`
	}
	if err := read("workflow.json", &runtime); err != nil {
		return fmt.Errorf("runtime settings unavailable")
	}
	row.Memory = row.Origin == "work"
	if runtime.Capabilities.Memory != nil {
		row.Memory = *runtime.Capabilities.Memory
	}
	row.LocalMetadata = row.Mode == "local"
	if row.LocalMetadata {
		row.Memory = false
	}
	return nil
}

func (api *StreamingAPI) crewStoragePreview(s *crewStorageSnapshot) {
	defer crewStorageJobs.Delete(s.ID)
	defer crewStorageJobs.Delete("active")
	dir, _ := crewStorageDir(s.ID)
	all, err := defaultProjectOwners().All()
	if err != nil {
		s.State, s.Error = "failed", "The project registry could not be read."
		_ = crewStorageSave(s)
		return
	}
	s.RegistrySHA, s.Records = crewStorageHash(all), all
	keys := make([]string, 0, len(all))
	for key := range all {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	docs := fsutil.WorkspaceDocsRoot()
	known := map[string]bool{}
	for _, key := range keys {
		rec := all[key]
		if !isProjectProfileID(rec.Product) {
			continue
		}
		source := projectStorageRoot(rec)
		row := crewStorageProject{Key: key, WorkspaceID: rec.Product + ":" + rec.ProjectID, Name: rec.Folder, Owner: rec.OwnerID, Origin: rec.Product, Source: source, Destination: "Crew/" + rec.Folder, RecordHash: crewStorageHash(rec), State: "ready"}
		row.OwnerName = crewOwnerDisplayName(rec.OwnerID)
		known[source] = true
		for _, alias := range rec.Aliases {
			known[alias] = true
		}
		if err := validateProjectStorageRecord(rec); err != nil || rec.OwnerID == "" || sanitizeUserIDForPath(rec.OwnerID) != rec.OwnerID || filepath.Base(rec.Folder) != rec.Folder || strings.HasPrefix(rec.Folder, ".") {
			row.State, row.Reasons = "needs_attention", []string{"The server record needs repair."}
		} else if source == row.Destination {
			row.State = "already_organised"
			if err := crewStorageReadSettings(filepath.Join(docs, filepath.FromSlash(source)), &row); err != nil {
				row.State, row.Reasons = "needs_attention", []string{err.Error()}
			}
		} else {
			full := filepath.Join(docs, filepath.FromSlash(source))
			real, err := filepath.EvalSymlinks(full)
			if err != nil || real != full {
				row.State, row.Reasons = "needs_attention", []string{"The source folder is missing or is a link."}
			} else if _, err := os.Lstat(filepath.Join(docs, filepath.FromSlash(row.Destination))); !os.IsNotExist(err) {
				row.State, row.Reasons = "needs_attention", []string{"The destination is already present or cannot be checked."}
			} else if err := crewStorageReadSettings(full, &row); err != nil {
				row.State, row.Reasons = "needs_attention", []string{err.Error()}
			} else {
				name := filepath.Join(dir, crewStorageHash(key)+".inventory.jsonl")
				manifest, err := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
				if err == nil {
					row.Fingerprint, row.Files, row.Bytes, err = crewStorageInventory(full, manifest)
					closeErr := manifest.Close()
					if err == nil {
						err = closeErr
					}
				}
				if err != nil {
					row.State, row.Reasons = "needs_attention", []string{"Some files or metadata could not be inventoried; this project is not backed up."}
				}
			}
		}
		s.Projects = append(s.Projects, row)
		if err := crewStorageSave(s); err != nil {
			return
		}
	}
	// Include unregistered roots instead of silently declaring the registry
	// complete. Never read their private file contents or infer an owner.
	for _, pattern := range []string{"_users/*/Chats/Code/projects/*", "_users/*/Chats/Work/projects/*", "Crew/*"} {
		matches, _ := filepath.Glob(filepath.Join(docs, filepath.FromSlash(pattern)))
		for _, full := range matches {
			rel, _ := filepath.Rel(docs, full)
			rel = filepath.ToSlash(rel)
			if known[rel] {
				continue
			}
			s.Projects = append(s.Projects, crewStorageProject{Key: "unregistered:" + rel, Name: filepath.Base(full), Source: rel, State: "needs_attention", Reasons: []string{"This folder has no matching server project record."}})
		}
	}
	s.State = "complete"
	_ = crewStorageSave(s)
	log.Printf("[CREW_STORAGE] operation=%s action=preview actor=%s state=complete projects=%d", s.ID, s.Actor, len(s.Projects))
}

func crewStorageArchive(ctx context.Context, s *crewStorageSnapshot, row *crewStorageProject, verifyRestore bool) error {
	dir, err := crewStorageDir(s.ID)
	if err != nil {
		return err
	}
	root := filepath.Join(fsutil.WorkspaceDocsRoot(), filepath.FromSlash(row.Source))
	unlock := lockPublication("target:" + row.Source)
	defer unlock()
	var before string
	if verifyRestore {
		before, _, _, err = crewStorageInventory(root, io.Discard)
		if err != nil || before != row.Fingerprint {
			return fmt.Errorf("files changed since preview; start a fresh preview")
		}
	}
	name := filepath.Join(dir, crewStorageHash(row.Key)+".tar.gz")
	free, err := crewStorageFreeBytes(dir)
	if err != nil {
		return fmt.Errorf("backup space could not be checked")
	}
	// Leave space for normal work, plus a scratch copy only when requested.
	reserve := uint64(1 << 30)
	if verifyRestore {
		reserve += uint64(row.Bytes)
	}
	if free <= reserve {
		return fmt.Errorf("there is not enough free space to verify this backup")
	}
	file, err := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	archiveVerified := false
	defer func() {
		if !archiveVerified {
			_ = os.Remove(name)
		}
	}()
	cmd := exec.CommandContext(ctx, "tar", "--sort=name", "--format=pax", "--numeric-owner", "--acls", "--xattrs", "--xattrs-include=*", "--sparse", "-czf", "-", "-C", filepath.Dir(root), filepath.Base(root))
	cmd.Stdout = &crewStorageArchiveWriter{output: file, remaining: int64(free - reserve)}
	runErr := cmd.Run()
	syncErr, closeErr := file.Sync(), file.Close()
	if !verifyRestore && runErr != nil {
		var exitErr *exec.ExitError
		// GNU tar reports files changing during creation with status 1. The
		// archive is still checked below; rename keeps the current live tree.
		if errors.As(runErr, &exitErr) && exitErr.ExitCode() == 1 {
			log.Printf("[CREW_STORAGE] operation=%s action=archive workspace=%s state=files_changed_during_archive", s.ID, row.WorkspaceID)
			runErr = nil
		}
	}
	if runErr != nil || syncErr != nil || closeErr != nil {
		return fmt.Errorf("the archive could not be completed within the available backup space")
	}
	row.ArchiveSHA, row.ArchiveBytes, err = crewStorageFileHash(name)
	if err != nil {
		return err
	}
	if !verifyRestore {
		// A folder rename preserves its original ownership. Check the saved
		// archive without requiring privilege to recreate another UID's files.
		cmd = exec.CommandContext(ctx, "tar", "-tzf", name)
		cmd.Stdout = io.Discard
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("the saved archive could not be read completely")
		}
		archiveVerified = true
		return nil
	}
	// Restoring in private server state proves readable bytes and actual
	// ownership/ACL restoration under the existing service privileges.
	probe, err := os.MkdirTemp(dir, ".restore-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(probe)
	cmd = exec.CommandContext(ctx, "tar", "--same-owner", "--same-permissions", "--acls", "--xattrs", "--xattrs-include=*", "--delay-directory-restore", "-xzf", name, "-C", probe)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("the existing service cannot restore this archive with its original ownership")
	}
	restored, _, _, err := crewStorageInventory(filepath.Join(probe, filepath.Base(root)), io.Discard)
	if err != nil || restored != before {
		return fmt.Errorf("restored contents or metadata do not match the original")
	}
	after, _, _, err := crewStorageInventory(root, io.Discard)
	if err != nil || after != before {
		return fmt.Errorf("the source changed during backup; start a fresh preview")
	}
	row.RestoreVerified = true
	archiveVerified = true
	return nil
}

func (api *StreamingAPI) crewStorageBackup(s *crewStorageSnapshot, selected string) {
	defer crewStorageJobs.Delete(s.ID)
	defer crewStorageJobs.Delete("active")
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Hour)
	defer cancel()
	for i := range s.Projects {
		row := &s.Projects[i]
		if row.State != "ready" || selected != "" && row.WorkspaceID != selected {
			continue
		}
		if row.RestoreVerified {
			continue
		}
		current, exists := defaultProjectOwners().Lookup(row.Origin, s.Records[row.Key].Folder)
		if !exists || crewStorageHash(current) != row.RecordHash {
			row.BackupState, row.Reasons = "not_backed_up", []string{"The project record changed. Start a fresh preview."}
		} else if err := crewStorageArchive(ctx, s, row, true); err != nil {
			row.BackupState, row.Reasons = "not_backed_up", []string{err.Error()}
		} else {
			row.BackupState = "project_archive_verified"
		}
		if err := crewStorageSave(s); err != nil {
			return
		}
	}
	s.State = "complete"
	_ = crewStorageSave(s)
	log.Printf("[CREW_STORAGE] operation=%s action=backup actor=%s state=complete", s.ID, s.BackupActor)
}

// Keep the current tree and its metadata intact. Existing routing aliases and
// a filesystem link preserve old references; private state keeps BindingRoot.
// This deliberately has no project-busy check or undo/redo operation.
func crewStorageMoveProject(s *crewStorageSnapshot, row *crewStorageProject) error {
	registry := defaultProjectOwners()
	rec, exists := registry.Lookup(row.Origin, s.Records[row.Key].Folder)
	if !exists || validateProjectStorageRecord(rec) != nil || rec.OwnerID != row.Owner || rec.Product+":"+rec.ProjectID != row.WorkspaceID || projectStorageRoot(rec) != row.Source || row.Destination != "Crew/"+rec.Folder {
		return fmt.Errorf("the project location changed; start a fresh preview")
	}
	docs := fsutil.WorkspaceDocsRoot()
	source := filepath.Join(docs, filepath.FromSlash(row.Source))
	destination := filepath.Join(docs, filepath.FromSlash(row.Destination))
	info, err := os.Lstat(source)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("the original project folder is unavailable")
	}
	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		return fmt.Errorf("the destination already exists or cannot be checked")
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0711); err != nil {
		return err
	}
	link, err := filepath.Rel(filepath.Dir(source), destination)
	if err != nil {
		return err
	}
	if err := os.Rename(source, destination); err != nil {
		return fmt.Errorf("the project folder could not be moved: %w", err)
	}
	// Create the compatibility link even if the registry write fails, so its
	// previous path still reaches the moved files. Report failures as they are.
	registryErr := registry.compareAndSetStorageLocation(rec.Product, rec.Folder, userAccessHash(rec), row.Destination)
	linkErr := os.Symlink(link, source)
	return errors.Join(registryErr, linkErr)
}

func (api *StreamingAPI) crewStorageMove(s *crewStorageSnapshot, selected string) {
	defer crewStorageJobs.Delete(s.ID)
	defer crewStorageJobs.Delete("active")
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Hour)
	defer cancel()
	for i := range s.Projects {
		row := &s.Projects[i]
		if row.State != "ready" || selected != "" && row.WorkspaceID != selected {
			continue
		}
		var err error
		if !row.RestoreVerified && row.BackupState != "archive_verified" {
			err = crewStorageArchive(ctx, s, row, false)
			if err == nil {
				row.BackupState = "archive_verified"
			} else {
				row.BackupState = "not_backed_up"
			}
		}
		if err == nil {
			dir, _ := crewStorageDir(s.ID)
			archive, statErr := os.Stat(filepath.Join(dir, crewStorageHash(row.Key)+".tar.gz"))
			if statErr != nil || !archive.Mode().IsRegular() || archive.Size() != row.ArchiveBytes || row.ArchiveSHA == "" {
				err = fmt.Errorf("the verified backup is unavailable; start a fresh preview")
			}
		}
		if err == nil {
			err = crewStorageMoveProject(s, row)
		}
		if err != nil {
			row.State, row.Reasons = "move_failed", []string{err.Error()}
		} else {
			row.State, row.Reasons = "moved", nil
		}
		log.Printf("[CREW_STORAGE] operation=%s action=move actor=%s workspace=%s source=%s destination=%s state=%s reason=%v", s.ID, s.MoveActor, row.WorkspaceID, row.Source, row.Destination, row.State, err)
		if err := crewStorageSave(s); err != nil {
			return
		}
	}
	s.State = "complete"
	_ = crewStorageSave(s)
	log.Printf("[CREW_STORAGE] operation=%s action=move actor=%s state=complete", s.ID, s.MoveActor)
}

type crewStorageArchiveWriter struct {
	output    io.Writer
	remaining int64
}

func (w *crewStorageArchiveWriter) Write(data []byte) (int, error) {
	if int64(len(data)) > w.remaining {
		return 0, fmt.Errorf("backup capacity reached")
	}
	n, err := w.output.Write(data)
	w.remaining -= int64(n)
	return n, err
}

func (api *StreamingAPI) externalCrewStorage(w http.ResponseWriter, r *http.Request, args map[string]any) {
	claims := GetUserFromContext(r.Context())
	if !crewStorageAllowed(claims) || !currentUserIsAdmin(r) {
		externalError(w, 403, "insufficient_scope", "An administrator can check, back up or move project storage with users:manage access.")
		return
	}
	action := externalArg(args, "action")
	if action == "storage_preview" {
		s := &crewStorageSnapshot{ID: uuid.NewString(), Actor: claims.UserID, CreatedAt: time.Now().UTC().Format(time.RFC3339), State: "scanning", Projects: []crewStorageProject{}}
		if active, loaded := crewStorageJobs.LoadOrStore("active", s.ID); loaded {
			state := "working"
			if current, err := crewStorageRead(fmt.Sprint(active)); err == nil {
				state = current.State
			}
			externalJSON(w, map[string]any{"operation_id": active, "state": state, "message": "A storage check is already working. Use storage_status for its result."})
			return
		}
		crewStorageJobs.Store(s.ID, true)
		if err := crewStorageSave(s); err != nil {
			crewStorageJobs.Delete(s.ID)
			crewStorageJobs.Delete("active")
			externalError(w, 500, "storage_unavailable", "The private preview could not be saved.")
			return
		}
		go api.crewStoragePreview(s)
		externalJSON(w, map[string]any{"operation_id": s.ID, "state": "scanning", "message": "Checking project files. No project is moved."})
		return
	}
	id := externalArg(args, "operation_id")
	if action == "storage_backup" || action == "storage_move" {
		s, err := crewStorageAdmitBackup(id, crewStorageRead)
		if err != nil {
			if errors.Is(err, errCrewStorageBusy) {
				externalError(w, 409, "storage_busy", "A storage check is already working. Check its status and retry when it finishes.")
			} else {
				externalError(w, 409, "storage_incomplete", "Start a fresh completed project check first.")
			}
			return
		}
		selected := externalArg(args, "workspace_id")
		if selected != "" {
			found := false
			for _, row := range s.Projects {
				if row.WorkspaceID == selected && row.State == "ready" {
					found = true
					break
				}
			}
			if !found {
				crewStorageJobs.Delete(id)
				crewStorageJobs.Delete("active")
				externalError(w, 400, "invalid_arguments", "Choose a project marked ready.")
				return
			}
		}
		s.BackupActor = claims.UserID
		s.State = "backing_up"
		if action == "storage_move" {
			s.MoveActor, s.State = claims.UserID, "moving"
		}
		if err := crewStorageSave(s); err != nil {
			crewStorageJobs.Delete(id)
			crewStorageJobs.Delete("active")
			externalError(w, 500, "storage_unavailable", "The backup could not be started.")
			return
		}
		if action == "storage_move" {
			go api.crewStorageMove(s, selected)
			externalJSON(w, map[string]any{"operation_id": id, "state": "moving", "message": "Moving ready projects to Crew, using verified backups and keeping old links working."})
			return
		}
		go api.crewStorageBackup(s, selected)
		externalJSON(w, map[string]any{"operation_id": id, "state": "backing_up", "message": "Backing up project files and checking a private restore. Projects stay in place."})
		return
	}
	if action != "storage_status" {
		externalError(w, 400, "invalid_arguments", "Choose storage_preview, storage_status, storage_backup or storage_move.")
		return
	}
	s, err := crewStorageRead(id)
	if err != nil {
		externalError(w, 404, "not_found", "Storage preview unavailable. Start a fresh preview.")
		return
	}
	offset, limit := externalInt(args, "offset", 0), externalInt(args, "limit", 25)
	if offset < 0 {
		offset = 0
	}
	if offset > len(s.Projects) {
		offset = len(s.Projects)
	}
	if limit < 1 {
		limit = 25
	}
	if limit > 100 {
		limit = 100
	}
	end := offset + limit
	if end > len(s.Projects) {
		end = len(s.Projects)
	}
	externalJSON(w, map[string]any{"operation_id": id, "state": s.State, "created_at": s.CreatedAt, "registry_sha256": s.RegistrySHA, "projects": s.Projects[offset:end], "total": len(s.Projects), "next_offset": end, "has_more": end < len(s.Projects), "error": s.Error, "move_available": true})
}

func crewStorageAllowed(claims *UserClaims) bool {
	return externalUsersAllowed(claims) && !userAccessForClaims(claims).Disabled &&
		(claims.AccessToken == nil || claims.AccessToken.BoundAgentName == "")
}

func (api *StreamingAPI) handleCrewStorage(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	args := map[string]any{}
	if r.Method == http.MethodGet {
		args["action"], args["operation_id"] = "storage_status", r.URL.Query().Get("operation_id")
		for _, name := range []string{"offset", "limit"} {
			var n int
			if _, err := fmt.Sscan(r.URL.Query().Get(name), &n); err == nil {
				args[name] = n
			}
		}
	} else if err := json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(&args); err != nil {
		externalError(w, 400, "invalid_arguments", "Choose a storage preview or backup.")
		return
	}
	api.externalCrewStorage(w, r, args)
}

var errCrewStorageBusy = errors.New("storage check is working")

// Admission owns both locks before loading a snapshot. A delayed retry cannot
// overwrite a completed worker's newer archive/restore receipt with its old view.
func crewStorageAdmitBackup(id string, read func(string) (*crewStorageSnapshot, error)) (*crewStorageSnapshot, error) {
	if _, err := crewStorageDir(id); err != nil {
		return nil, err
	}
	if _, loaded := crewStorageJobs.LoadOrStore(id, true); loaded {
		return nil, errCrewStorageBusy
	}
	if _, loaded := crewStorageJobs.LoadOrStore("active", id); loaded {
		crewStorageJobs.Delete(id)
		return nil, errCrewStorageBusy
	}
	s, err := read(id)
	if err != nil || s.State != "complete" {
		crewStorageJobs.Delete(id)
		crewStorageJobs.Delete("active")
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("preview is incomplete")
	}
	return s, nil
}
