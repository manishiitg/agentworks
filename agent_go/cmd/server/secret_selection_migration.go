package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/chathistory"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
	"github.com/spf13/cobra"
)

const secretSelectionMigrationVersion = "secret-selections-v1"

type secretSelectionMigrationOptions struct {
	DocsRoot, StateRoot string
	Apply, Once         bool
}

type secretSelectionMigrationChange struct {
	Manifest  string              `json:"manifest"`
	Removed   map[string][]string `json:"removed"`
	Backup    string              `json:"backup,omitempty"`
	raw, next []byte
	mode      fs.FileMode
}

type secretSelectionMigrationReport struct {
	DocsRoot        string                           `json:"docs_root"`
	Applied         bool                             `json:"applied"`
	AlreadyComplete bool                             `json:"already_complete"`
	Scanned         int                              `json:"scanned"`
	Changes         []secretSelectionMigrationChange `json:"changes"`
}

var migrateSecretSelectionsCmd = &cobra.Command{
	Use:   "migrate-secret-selections",
	Short: "Remove legacy secret selections that have no project or global record",
	Long:  `Inspect canonical workflow and project manifests. Remove only selected names absent from the project's shared/legacy secret stores and from the global encrypted/environment stores. Existing records are retained even when unreadable or ungranted; no values are decrypted, moved, printed or granted. An unreadable or malformed store aborts the scan before any write. Default is a dry run. Stop the agent before --apply. Changed manifests receive private backups; --once records completion outside releases.`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		docs, _ := cmd.Flags().GetString("docs-root")
		state, _ := cmd.Flags().GetString("state-root")
		apply, _ := cmd.Flags().GetBool("apply")
		once, _ := cmd.Flags().GetBool("once")
		if docs == "" {
			docs = fsutil.WorkspaceDocsRoot()
		}
		if state == "" {
			var err error
			state, err = workflowCLIStateRoot()
			if err != nil {
				return err
			}
		}
		report, err := runSecretSelectionMigration(secretSelectionMigrationOptions{DocsRoot: docs, StateRoot: state, Apply: apply, Once: once})
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(report)
	},
}

func init() {
	migrateSecretSelectionsCmd.Flags().String("docs-root", "", "Workspace docs directory (defaults to WORKSPACE_DOCS_PATH)")
	migrateSecretSelectionsCmd.Flags().String("state-root", "", "Private deployment state directory for backups and completion")
	migrateSecretSelectionsCmd.Flags().Bool("apply", false, "Back up and repair manifests (default: dry run)")
	migrateSecretSelectionsCmd.Flags().Bool("once", false, "Skip an already completed migration for this docs root")
}

// This is also used by local launches and installers without an activation hook.
// It runs before the HTTP listener, and only for a Vault-enabled installation.
func migrateSecretSelectionsAtStartup(stateRoot string) {
	if strings.TrimSpace(os.Getenv("CAPLAYER_SERVICE_URL")) == "" {
		return
	}
	r, err := runSecretSelectionMigration(secretSelectionMigrationOptions{DocsRoot: fsutil.WorkspaceDocsRoot(), StateRoot: stateRoot, Apply: true, Once: true})
	if err != nil {
		log.Printf("[SECRET_SELECTION_MIGRATION] no completion marker: %v; retry on next start", err)
		return
	}
	if !r.AlreadyComplete {
		log.Printf("[SECRET_SELECTION_MIGRATION] scanned=%d repaired=%d; backups/report in %s", r.Scanned, len(r.Changes), filepath.Join(stateRoot, "migrations", secretSelectionMigrationVersion))
	}
}

// Opens every component without accepting symlinks, then anchors all IO to that
// directory. Comparing the opened inode also refuses a replacement during open.
func secretMigrationDir(root *os.Root, rel string) (*os.Root, error) {
	current, err := root.OpenRoot(".")
	if err != nil {
		return nil, err
	}
	if rel == "." {
		return current, nil
	}
	if !filepath.IsLocal(rel) {
		current.Close()
		return nil, errors.New("unsafe migration path")
	}
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		info, err := current.Lstat(part)
		if err != nil {
			current.Close()
			return nil, err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			current.Close()
			return nil, fmt.Errorf("unsafe migration directory: %s", rel)
		}
		next, err := current.OpenRoot(part)
		if err != nil {
			current.Close()
			return nil, err
		}
		opened, err := next.Stat(".")
		current.Close()
		if err != nil || !os.SameFile(info, opened) {
			next.Close()
			return nil, errors.New("migration directory changed during scan")
		}
		current = next
	}
	return current, nil
}

func secretMigrationRead(root *os.Root, rel string) ([]byte, fs.FileMode, error) {
	dir, err := secretMigrationDir(root, filepath.Dir(rel))
	if err != nil {
		return nil, 0, err
	}
	defer dir.Close()
	name := filepath.Base(rel)
	info, err := dir.Lstat(name)
	if err != nil {
		return nil, 0, err
	}
	if !info.Mode().IsRegular() || info.Size() > 16<<20 {
		return nil, 0, fmt.Errorf("unsafe or oversized migration file: %s", rel)
	}
	f, err := dir.Open(name)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, 0, errors.New("migration file changed during scan")
	}
	raw, err := io.ReadAll(io.LimitReader(f, (16<<20)+1))
	if len(raw) > 16<<20 {
		return nil, 0, errors.New("migration file grew during scan")
	}
	return raw, info.Mode().Perm(), err
}

func secretMigrationChildren(root *os.Root, rel string) ([]string, error) {
	dir, err := secretMigrationDir(root, rel)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	f, err := dir.Open(".")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	entries, err := f.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("symlink in migration root: %s/%s", rel, entry.Name())
		}
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

func secretMigrationManifests(root *os.Root) ([]string, error) {
	roots := []string{"Workflow", workspaceref.SharedCrewRoot, "Relays"}
	projects := append([]string{}, workspaceref.ProjectRoots...)
	projects = append(projects, "Chats/Relays/projects", "Chats/Goals/projects", "Chats/Video Studio/projects")
	roots = append(roots, projects...)
	users, err := secretMigrationChildren(root, filepath.Dir(workspaceref.UserRoot("default")))
	if err != nil {
		return nil, err
	}
	for _, user := range users {
		if user == chathistory.SharedWorkflowSecretsUserID || user == managedGlobalSecretsUserID {
			continue
		}
		for _, project := range append([]string{"Workflow"}, projects...) {
			roots = append(roots, workspaceref.PhysicalPath(user, project))
		}
	}
	var paths []string
	for _, rel := range roots {
		children, err := secretMigrationChildren(root, rel)
		if err != nil {
			return nil, err
		}
		for _, child := range children {
			for _, name := range []string{"workflow.json", "product.json"} {
				path := filepath.Join(rel, child, name)
				if _, _, err := secretMigrationRead(root, path); err == nil {
					paths = append(paths, path)
				} else if !errors.Is(err, fs.ErrNotExist) {
					return nil, err
				}
			}
		}
	}
	sort.Strings(paths)
	return paths, nil
}

func secretMigrationStoredNames(root *os.Root, rel, workspace string) (map[string]bool, error) {
	names := map[string]bool{}
	raw, _, err := secretMigrationRead(root, rel)
	if errors.Is(err, fs.ErrNotExist) {
		return names, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cannot inspect secret metadata %s: %w", rel, err)
	}
	var records map[string]json.RawMessage
	if json.Unmarshal(raw, &records) != nil || records == nil {
		return nil, fmt.Errorf("malformed secret store: %s", rel)
	}
	// Managed global/user stores are a direct name-to-record map. Project
	// stores wrap that map with a workflow_path; keep these formats distinct.
	if workspace != "" {
		var storedWorkspace string
		if json.Unmarshal(records["workflow_path"], &storedWorkspace) != nil || storedWorkspace != workspace {
			return nil, fmt.Errorf("secret store belongs to another workspace: %s", rel)
		}
		data, exists := records["secrets"]
		if !exists || json.Unmarshal(data, &records) != nil {
			return nil, fmt.Errorf("malformed project secret store: %s", rel)
		}
	}
	// A record, including a corrupt/empty one, requires repair rather than deletion.
	for name := range records {
		names[name] = true
	}
	return names, nil
}

func runSecretSelectionMigration(opts secretSelectionMigrationOptions) (*secretSelectionMigrationReport, error) {
	docs, err := filepath.Abs(opts.DocsRoot)
	if err != nil || opts.DocsRoot == "" {
		return nil, errors.New("docs-root is required")
	}
	state, err := filepath.Abs(opts.StateRoot)
	if err != nil || opts.StateRoot == "" {
		return nil, errors.New("state-root is required")
	}
	r := &secretSelectionMigrationReport{DocsRoot: docs, Changes: []secretSelectionMigrationChange{}}
	migration := filepath.Join(state, "migrations", secretSelectionMigrationVersion)
	marker := filepath.Join(migration, "completed.json")
	if opts.Apply && opts.Once {
		if raw, err := os.ReadFile(marker); err == nil {
			var done secretSelectionMigrationReport
			if json.Unmarshal(raw, &done) != nil || done.DocsRoot != docs {
				return nil, errors.New("migration marker has a different or invalid docs root")
			}
			r.AlreadyComplete = true
			return r, nil
		} else if !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
	}
	root, err := os.OpenRoot(docs)
	if err != nil {
		return nil, fmt.Errorf("cannot open docs root: %w", err)
	}
	defer root.Close()
	globals, err := secretMigrationStoredNames(root, workspaceref.PhysicalPath(managedGlobalSecretsUserID, "secrets.json"), "")
	if err != nil {
		return nil, err
	}
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "GLOBAL_SECRET_") {
			globals[strings.TrimPrefix(key, "GLOBAL_SECRET_")] = true
		}
	}
	paths, err := secretMigrationManifests(root)
	if err != nil {
		return nil, err
	}
	// Legacy manifests can predate owner metadata. An encrypted record under
	// any user for this exact project path is evidence that a value exists;
	// retaining its selection grants no authority to read or use that record.
	legacyUsers, err := secretMigrationChildren(root, filepath.Dir(workspaceref.UserRoot("default")))
	if err != nil {
		return nil, err
	}
	for _, path := range paths {
		raw, mode, err := secretMigrationRead(root, path)
		if err != nil {
			return nil, err
		}
		var manifest map[string]json.RawMessage
		if json.Unmarshal(raw, &manifest) != nil || manifest == nil {
			return nil, fmt.Errorf("malformed manifest: %s", path)
		}
		r.Scanned++
		capsRaw, ok := manifest["capabilities"]
		if !ok || bytes.Equal(bytes.TrimSpace(capsRaw), []byte("null")) {
			continue
		}
		var caps map[string]json.RawMessage
		if json.Unmarshal(capsRaw, &caps) != nil {
			return nil, fmt.Errorf("malformed capabilities: %s", path)
		}
		workspace, err := chathistory.NormalizeWorkflowSecretPath(filepath.ToSlash(filepath.Dir(path)))
		if err != nil {
			return nil, err
		}
		owners := []string{chathistory.SharedWorkflowSecretsUserID}
		for _, user := range legacyUsers {
			if user != chathistory.SharedWorkflowSecretsUserID && user != managedGlobalSecretsUserID {
				owners = append(owners, user)
			}
		}
		available := map[string]bool{}
		for name := range globals {
			available[name] = true
		}
		hash := sha256.Sum256([]byte(workspace))
		for _, owner := range owners {
			names, err := secretMigrationStoredNames(root, workspaceref.PhysicalPath(owner, "workflow_secrets", hex.EncodeToString(hash[:])+".json"), workspace)
			if err != nil {
				return nil, err
			}
			for name := range names {
				available[name] = true
			}
		}
		change := secretSelectionMigrationChange{Manifest: filepath.ToSlash(path), Removed: map[string][]string{}, raw: raw, mode: mode}
		for _, field := range []string{"selected_secrets", "selected_global_secret_names"} {
			var names []string
			if data, ok := caps[field]; !ok || bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
				continue
			} else if json.Unmarshal(data, &names) != nil {
				return nil, fmt.Errorf("invalid %s in %s", field, path)
			}
			kept := []string{}
			for _, name := range names {
				if available[name] {
					kept = append(kept, name)
				} else {
					change.Removed[field] = append(change.Removed[field], name)
				}
			}
			if len(change.Removed[field]) != 0 {
				caps[field], _ = json.Marshal(kept)
			}
		}
		if len(change.Removed) == 0 {
			continue
		}
		manifest["capabilities"], _ = json.Marshal(caps)
		change.next, err = json.MarshalIndent(manifest, "", "  ")
		if err != nil {
			return nil, err
		}
		change.next = append(change.next, '\n')
		r.Changes = append(r.Changes, change)
	}
	if !opts.Apply {
		return r, nil
	}
	// Finish the entire scan before writing anything. Backups precede replacements.
	if err := os.MkdirAll(migration, 0700); err != nil {
		return nil, err
	}
	backupRoot, err := os.MkdirTemp(migration, "backups-")
	if err != nil {
		return nil, err
	}
	for i := range r.Changes {
		change := &r.Changes[i]
		hash := sha256.Sum256([]byte(change.Manifest))
		change.Backup = filepath.Join(backupRoot, hex.EncodeToString(hash[:])+".json")
		if err := os.WriteFile(change.Backup, change.raw, 0600); err != nil {
			return nil, err
		}
	}
	for _, change := range r.Changes {
		current, _, err := secretMigrationRead(root, change.Manifest)
		if err != nil || !bytes.Equal(current, change.raw) {
			return nil, fmt.Errorf("manifest changed during migration: %s (stop the agent and retry)", change.Manifest)
		}
		if err := secretMigrationReplace(root, change.Manifest, change.next, change.mode); err != nil {
			return nil, err
		}
	}
	r.Applied = true
	report, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return nil, err
	}
	// Retain a report even without --once. Completion is written only on success.
	if err := os.WriteFile(filepath.Join(backupRoot, "report.json"), report, 0600); err != nil {
		return nil, err
	}
	if opts.Once {
		if err := writeFileAtomically(marker, report); err != nil {
			return nil, err
		}
		if err := os.Chmod(marker, 0600); err != nil {
			return nil, err
		}
	}
	return r, nil
}

func secretMigrationReplace(root *os.Root, rel string, content []byte, mode fs.FileMode) error {
	dir, err := secretMigrationDir(root, filepath.Dir(rel))
	if err != nil {
		return err
	}
	defer dir.Close()
	tmp := fmt.Sprintf(".secret-selections-%d-%d.tmp", os.Getpid(), time.Now().UnixNano())
	f, err := dir.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	defer dir.Remove(tmp)
	_, err = f.Write(content)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return dir.Rename(tmp, filepath.Base(rel))
}
