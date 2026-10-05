package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
)

// PLAT-442 step 4: the Crew move. `agentworks server migrate-crews-to-shared-root` moves each Crew from its owner's
// private tree (_users/<owner>/Chats/Work/projects/<folder>) to the shared root Crew/<folder>, keeping the folder name.
//
// Safety, in the order it matters:
//  1. It does nothing unless --apply is given with a --backup-dir, and the backup of exactly the folders it will touch
//     is written and verified readable before the first move.
//  2. One Crew at a time, each through copy -> verify -> switch: the Crew is copied (never followed through a symlink,
//     see crew_move_tree.go) into Crew/.migrating/<folder>, every file is hashed and compared, the source is checked
//     unchanged, and only then are two renames inside the docs root switched in (new folder in, old folder renamed to a
//     tombstone beside its owner's projects). Nothing is deleted: the old folder stays as the tombstone until
//     --finalize.
//  3. Every step is recorded in a per-Crew journal (<state root>/migrations/crew-move/<folder>.json) before and after it
//     happens, so a crash resumes or rolls back; a re-run is idempotent.
//  4. A lock marker tells the server not to bind turns to, or write to, the Crew while it moves.
//  5. A Crew is refused (reported by the dry run, blocking --apply for that Crew) when: its manifest owner_id differs
//     from the owner its path names ([OWNER_MISMATCH]); Crew/<folder> exists; the folder name belongs to several owners;
//     a tmux session or a process works in it or a CLI runtime that links to it; it holds a symlink that leaves the
//     folder or a special file; or the disks lack the space.
//  6. Afterwards the owner registry records the Crew as shared with its old path as an alias (the alias list is what
//     keeps every old spelling resolving, indefinitely), CLI runtime links are repointed (the runtime folder, which is
//     named by a digest of the old path, is kept), and a verification pass re-hashes the moved Crew against the journal.

const (
	crewMoveJournalSubdir  = "migrations/crew-move"
	crewMoveStagingDir     = ".migrating"
	crewMoveTombstoneDir   = ".moved-to-crew"
	crewMoveJournalVersion = 1
)

type crewMoveState string

const (
	crewMovePlanned    crewMoveState = "planned"
	crewMoveStaged     crewMoveState = "staged"
	crewMoveVerified   crewMoveState = "verified"
	crewMoveSwitching  crewMoveState = "switching"
	crewMoveSwitched   crewMoveState = "switched"
	crewMoveRegistered crewMoveState = "registered"
	crewMoveDone       crewMoveState = "done"
	crewMoveRolledBack crewMoveState = "rolled_back"
)

type crewTreeSummary struct {
	Files    int    `json:"files"`
	Dirs     int    `json:"dirs"`
	Symlinks int    `json:"symlinks"`
	Bytes    int64  `json:"bytes"`
	Digest   string `json:"digest,omitempty"`
}

// crewMoveJournal is the per-Crew record that makes the move resumable and reversible.
type crewMoveJournal struct {
	Version   int           `json:"version"`
	Folder    string        `json:"folder"`
	Owner     string        `json:"owner"`
	ProjectID string        `json:"project_id,omitempty"`
	Source    string        `json:"source"`
	Staging   string        `json:"staging"`
	Dest      string        `json:"dest"`
	Tombstone string        `json:"tombstone"`
	OldAbs    string        `json:"old_abs,omitempty"`
	NewAbs    string        `json:"new_abs,omitempty"`
	State     crewMoveState `json:"state"`
	// RootMode and RootGID are the Crew folder's own mode and group when it was planned (verified after the move).
	RootMode        uint32          `json:"root_mode,omitempty"`
	RootGID         int             `json:"root_gid"`
	Tree            crewTreeSummary `json:"tree"`
	BackupRun       string          `json:"backup_run,omitempty"`
	StartedAt       string          `json:"started_at"`
	UpdatedAt       string          `json:"updated_at"`
	LastError       string          `json:"last_error,omitempty"`
	LinksRepointed  int             `json:"runtime_links_repointed,omitempty"`
	Finalized       bool            `json:"finalized,omitempty"`
	RolledBackAt    string          `json:"rolled_back_at,omitempty"`
	RollbackBackupC string          `json:"rollback_note,omitempty"`
}

// crewReferenceHit counts the stored references to a Crew's folder found in one kind of store.
type crewReferenceHit struct {
	Kind  string `json:"kind"`
	Files int    `json:"files"`
}

// crewMovePlan is what the dry run prints for one Crew.
type crewMovePlan struct {
	Folder        string   `json:"folder"`
	Owner         string   `json:"owner"`
	ProjectID     string   `json:"project_id,omitempty"`
	Title         string   `json:"title,omitempty"`
	Source        string   `json:"source"`
	Dest          string   `json:"dest"`
	Files         int      `json:"files"`
	Dirs          int      `json:"dirs"`
	Symlinks      int      `json:"symlinks"`
	Bytes         int64    `json:"bytes"`
	ManifestOwner string   `json:"manifest_owner,omitempty"`
	RegistryOwner string   `json:"registry_owner,omitempty"`
	RegistryState string   `json:"registry_state"`
	Readers       []string `json:"readers,omitempty"`
	// RuntimeFolders are CLI runtime folders whose project link points at the Crew (repointed by the move).
	RuntimeFolders int                `json:"cli_runtime_folders,omitempty"`
	References     []crewReferenceHit `json:"stored_references,omitempty"`
	Blockers       []string           `json:"blockers,omitempty"`
	Warnings       []string           `json:"warnings,omitempty"`
	State          string             `json:"state"`
}

// crewMoveReport is the whole run's result.
type crewMoveReport struct {
	Mode      string            `json:"mode"` // dry-run, apply, rollback, finalize
	DocsRoot  string            `json:"docs_root"`
	StateRoot string            `json:"state_root"`
	BackupRun string            `json:"backup_run,omitempty"`
	Crews     []crewMovePlan    `json:"crews"`
	Moved     []string          `json:"moved,omitempty"`
	Resumed   []string          `json:"resumed,omitempty"`
	Skipped   []string          `json:"skipped,omitempty"`
	Failed    map[string]string `json:"failed,omitempty"`
	Verified  []string          `json:"verified,omitempty"`
	Problems  []string          `json:"problems,omitempty"`
}

type crewMoveOptions struct {
	DocsRoot  string
	StateRoot string
	BackupDir string
	Apply     bool
	// Crews restricts the run to these folder names or project ids (empty: every Crew).
	Crews             []string
	Rollback          string
	FromBackup        bool
	Finalize          bool
	SkipReferenceScan bool
	Out               io.Writer

	// Test and tooling hooks.
	Probe     crewActivityProbe
	Now       func() time.Time
	FreeBytes func(path string) (uint64, error)
	// Hook is called at named points of a Crew's move; an error stops the run there, as a crash would.
	Hook func(point string) error
	// Readers lists the users with the Crew product (for the dry run); nil means the user directory.
	Readers func() []string
}

func (o *crewMoveOptions) now() time.Time {
	if o.Now != nil {
		return o.Now().UTC()
	}
	return time.Now().UTC()
}

func (o *crewMoveOptions) hook(point string) error {
	if o.Hook != nil {
		return o.Hook(point)
	}
	return nil
}

func (o *crewMoveOptions) free(path string) (uint64, error) {
	if o.FreeBytes != nil {
		return o.FreeBytes(path)
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}

var errCrewMoveIncomplete = errors.New("some Crews were blocked or failed; nothing was left half-moved (see the report)")

// ---- journals ----

func crewMoveJournalPath(stateRoot, folder string) string {
	return filepath.Join(crewMoveStateDir(stateRoot), folder+".json")
}

func loadCrewMoveJournal(stateRoot, folder string) (*crewMoveJournal, error) {
	raw, err := os.ReadFile(crewMoveJournalPath(stateRoot, folder))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var j crewMoveJournal
	if err := json.Unmarshal(raw, &j); err != nil {
		return nil, fmt.Errorf("journal of %s is unreadable: %w", folder, err)
	}
	return &j, nil
}

func (j *crewMoveJournal) save(stateRoot string, now time.Time) error {
	if err := os.MkdirAll(crewMoveStateDir(stateRoot), 0o700); err != nil {
		return err
	}
	j.Version = crewMoveJournalVersion
	j.UpdatedAt = now.Format(time.RFC3339)
	encoded, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return err
	}
	return writeRegistryFileAtomically(crewMoveJournalPath(stateRoot, j.Folder), append(encoded, '\n'))
}

func listCrewMoveJournals(stateRoot string) ([]*crewMoveJournal, error) {
	entries, err := os.ReadDir(crewMoveStateDir(stateRoot))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []*crewMoveJournal
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".json") || name == crewMoveActiveFile {
			continue
		}
		j, err := loadCrewMoveJournal(stateRoot, strings.TrimSuffix(name, ".json"))
		if err != nil {
			return nil, err
		}
		if j != nil {
			out = append(out, j)
		}
	}
	return out, nil
}

// ---- discovery and planning ----

// crewMoveCandidate is a Crew found in an owner's tree.
type crewMoveCandidate struct {
	plan     crewMovePlan
	entries  []crewTreeEntry
	rootMode fs.FileMode
	rootGID  int
}

func crewSourceRel(owner, folder string) string {
	return workspaceref.PhysicalPathOf(owner, workspaceref.CrewProjectsRoot, folder)
}

func crewTombstoneRel(owner, folder string) string {
	return workspaceref.PhysicalPathOf(owner, "Chats", "Work", crewMoveTombstoneDir, folder)
}

func crewStagingRel(folder string) string {
	return workspaceref.SharedProjectPath(crewMoveStagingDir, folder)
}

// discoverCrews lists the Crews in every owner's tree, with the findings that block or qualify each one. It reads
// nothing outside the owners' Crew folders and never follows a link.
func discoverCrews(docs *os.Root, registry *projectOwnerRegistry, opts *crewMoveOptions) ([]crewMoveCandidate, []string, error) {
	var problems []string
	users, err := listProjectDirNames(docs, workspaceref.UsersDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	var found []crewMoveCandidate
	owners := map[string][]string{} // folder -> owners
	for _, owner := range users {
		if sanitizeUserIDForPath(owner) != owner {
			continue
		}
		folders, err := listProjectNamesUnder(docs, owner, workspaceref.CrewProjectsRoot)
		if err != nil {
			if errors.Is(err, errUnsafeProjectPath) {
				problems = append(problems, fmt.Sprintf("[UNSAFE_PATH] %s: a symlink stands where %s should be a folder; its Crews are not listed", owner, workspaceref.CrewProjectsRoot))
			}
			continue
		}
		for _, folder := range folders {
			candidate := crewMoveCandidate{plan: crewMovePlan{
				Folder: folder, Owner: owner,
				Source: crewSourceRel(owner, folder), Dest: workspaceref.SharedProjectPath(folder),
				RegistryState: "none", State: "not started",
			}}
			plan := &candidate.plan
			project, err := openProjectDir(docs, owner, workspaceref.CrewProjectsRoot, folder)
			if err != nil {
				plan.Blockers = append(plan.Blockers, fmt.Sprintf("[UNSAFE_PATH] %v", err))
				found = append(found, candidate)
				continue
			}
			raw, _, manifestErr := readProjectManifest(project)
			var manifest struct {
				Product string `json:"product"`
				ID      string `json:"id"`
				Title   string `json:"title"`
				OwnerID string `json:"owner_id"`
			}
			switch {
			case manifestErr != nil:
				if errors.Is(manifestErr, fs.ErrNotExist) {
					_ = project.Close()
					continue // not a Crew (no manifest): nothing to move
				}
				plan.Blockers = append(plan.Blockers, fmt.Sprintf("[UNSAFE_PATH] product.json: %v", manifestErr))
			case json.Unmarshal([]byte(raw), &manifest) != nil:
				plan.Blockers = append(plan.Blockers, "product.json is not valid JSON")
			case !strings.EqualFold(strings.TrimSpace(manifest.Product), "work"):
				_ = project.Close()
				continue // another product's folder under the Crew root
			}
			plan.ProjectID, plan.Title, plan.ManifestOwner = strings.TrimSpace(manifest.ID), strings.TrimSpace(manifest.Title), cleanManifestOwner(manifest.OwnerID)
			if plan.ManifestOwner != "" && plan.ManifestOwner != owner {
				plan.Blockers = append(plan.Blockers, fmt.Sprintf("[OWNER_MISMATCH] product.json says owner_id %q but the folder is in %q's tree (a manifest copied between accounts keeps its old owner): confirm who owns this Crew, then set owner_id in product.json to the real owner (or remove the field) and re-run", plan.ManifestOwner, owner))
			}
			if rec, ok := registry.Lookup("work", folder); ok {
				plan.RegistryOwner = rec.OwnerID
				plan.RegistryState = "registered"
				if rec.Shared {
					plan.RegistryState = "shared"
				}
				if rec.OwnerID != owner {
					plan.Blockers = append(plan.Blockers, fmt.Sprintf("the owner registry has %q as the owner of %s, but the folder is in %q's tree", rec.OwnerID, folder, owner))
				}
			}
			entries, findings, walkErr := walkCrewTree(project)
			_ = project.Close()
			if walkErr != nil {
				plan.Blockers = append(plan.Blockers, fmt.Sprintf("cannot list the folder: %v", walkErr))
			}
			for _, finding := range findings {
				switch finding.Kind {
				case "external-symlink":
					plan.Blockers = append(plan.Blockers, fmt.Sprintf("symlink %s leaves the Crew folder (%s): remove or replace it", finding.Rel, finding.Detail))
				case "special-file":
					plan.Blockers = append(plan.Blockers, fmt.Sprintf("special file %s (%s) cannot be moved", finding.Rel, finding.Detail))
				case "sqlite-shm":
					plan.Warnings = append(plan.Warnings, fmt.Sprintf("%s is not copied (%s)", finding.Rel, finding.Detail))
				case "internal-symlink":
					plan.Warnings = append(plan.Warnings, fmt.Sprintf("symlink %s %s is copied as a symlink", finding.Rel, finding.Detail))
				}
			}
			summary := crewTreeSummaryOf(entries)
			plan.Files, plan.Dirs, plan.Symlinks, plan.Bytes = summary.Files, summary.Dirs, summary.Symlinks, summary.Bytes
			if len(entries) > 0 {
				candidate.entries = entries
				candidate.rootMode, candidate.rootGID = entries[0].Mode, entries[0].GID
			}
			owners[folder] = append(owners[folder], owner)
			found = append(found, candidate)
		}
	}
	for i := range found {
		plan := &found[i].plan
		if len(owners[plan.Folder]) > 1 {
			plan.Blockers = append(plan.Blockers, fmt.Sprintf("the folder name %q exists in %d owners' trees (%s): folder names must be unique at the shared root", plan.Folder, len(owners[plan.Folder]), strings.Join(owners[plan.Folder], ", ")))
		}
		if info, err := docs.Lstat(plan.Dest); err == nil {
			plan.Warnings = append(plan.Warnings, fmt.Sprintf("%s exists already (%s)", plan.Dest, info.Mode().Type()))
		}
	}
	sort.Slice(found, func(i, j int) bool {
		if found[i].plan.Owner != found[j].plan.Owner {
			return found[i].plan.Owner < found[j].plan.Owner
		}
		return found[i].plan.Folder < found[j].plan.Folder
	})
	return found, problems, nil
}

func selectedCrew(plan crewMovePlan, selectors []string) bool {
	if len(selectors) == 0 {
		return true
	}
	for _, s := range selectors {
		if s = strings.TrimSpace(s); s != "" && (s == plan.Folder || s == plan.ProjectID) {
			return true
		}
	}
	return false
}
