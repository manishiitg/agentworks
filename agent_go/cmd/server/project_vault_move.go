package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/chathistory"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// All in Vault (owner, 5 Nov): every secret still kept in a project's own vault moves into the project owner's own
// Vault (My accounts) and the project selects it there. It runs at startup on every server and Mac once Vault answers,
// and again until nothing is left to move; a project vault with no secrets left is done. The Brain keystore's vault
// is a system setting, not a product's, and stays. Logs carry names only, never values.

type projectVaultToMove struct {
	ID, Root, Holder string // Holder is the person who owns the project vault in Vault
	Names            []string
}

var projectLabelUnsafe = regexp.MustCompile(`[^A-Z0-9_]`)

// startProjectVaultMove runs the move now and, while a project could not be finished for a passing reason (Vault or
// the workspace not answering), again a minute later, up to ten times.
func (api *StreamingAPI) startProjectVaultMove() {
	go func() {
		for attempt := 1; attempt <= 10; attempt++ {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			retry, err := api.moveProjectVaultSecrets(ctx)
			cancel()
			if err != nil {
				log.Printf("[VAULT_MIGRATE] attempt=%d err=%v", attempt, err)
			}
			if err == nil && !retry {
				return
			}
			time.Sleep(time.Minute)
		}
	}()
}

// moveProjectVaultSecrets moves what is left in project vaults. retry reports a project skipped for a passing reason.
func (api *StreamingAPI) moveProjectVaultSecrets(ctx context.Context) (retry bool, err error) {
	if api.chatStore == nil || !vaultConfigured() {
		return false, nil
	}
	vaults := projectVaultsToMove(ctx)
	if len(vaults) == 0 {
		api.moveRetiredSecretSelections(ctx, nil)
		log.Printf("[VAULT_MIGRATE] result=nothing_to_move projects=0")
		return false, nil
	}
	backup, err := api.backupProjectVaultValues(ctx, vaults)
	if err != nil {
		return true, err
	}
	for _, vault := range vaults {
		if api.moveProjectVault(ctx, vault, backup) {
			retry = true
		}
	}
	log.Printf("[VAULT_MIGRATE] result=done projects=%d retry=%t", len(vaults), retry)
	if !retry {
		// A project that had a vault this time is swept at the next start, once its vault is empty.
		pending := map[string]bool{}
		for _, vault := range vaults {
			pending[vault.Root] = true
		}
		api.moveRetiredSecretSelections(ctx, pending)
	}
	return retry, nil
}

// moveRetiredSecretSelections moves names a project listed in its retired selected_secrets that are Vault secrets
// (a secret promoted to Vault, or a Crew created with a Vault secret) into its Vault selection, once. A name that
// still has a project copy waiting to move is left alone.
func (api *StreamingAPI) moveRetiredSecretSelections(ctx context.Context, pending map[string]bool) {
	roots := []string{}
	if workflows, err := DiscoverWorkflowManifests(ctx); err == nil {
		for _, item := range workflows {
			roots = append(roots, item.WorkspacePath)
		}
	}
	if records, err := defaultProjectOwners().All(); err == nil {
		for _, rec := range records {
			if rec.Product == crewProfileID && rec.Shared && strings.TrimSpace(rec.Folder) != "" {
				roots = append(roots, "Crew/"+rec.Folder)
			}
		}
	}
	backup := ""
	for _, root := range roots {
		if pending[root] {
			continue
		}
		manifest, err := readProjectSelectionManifest(ctx, root)
		if err != nil || !manifest.found {
			continue
		}
		capabilities, _ := manifest.doc["capabilities"].(map[string]any)
		if capabilities == nil {
			continue
		}
		changed := false
		for _, name := range manifestNames(capabilities["selected_secrets"]) {
			managedGlobalsMu.RLock()
			_, isVault := managedGlobals[name]
			managedGlobalsMu.RUnlock()
			if isVault && manifest.swap(name, name) {
				changed = true
				logVaultMove(root, name, name, "selected_existing", "")
			}
		}
		if !changed {
			continue
		}
		if backup == "" {
			state, err := workflowCLIStateRoot()
			if err != nil || strings.TrimSpace(state) == "" {
				return
			}
			backup = filepath.Join(state, "vault-migration-backups", time.Now().UTC().Format("20060102T150405Z")+"-selections")
		}
		if err := manifest.save(ctx, backup); err != nil {
			log.Printf("[VAULT_MIGRATE] project=%s selection not saved err=%v", root, err)
		}
	}
}

// projectVaultsToMove lists, for every person, the project vaults they hold that still have secrets.
func projectVaultsToMove(ctx context.Context) []projectVaultToMove {
	people := map[string]bool{}
	if directory, err := readUserDirectoryFile(); err == nil && directory != nil {
		for _, user := range directory.Users {
			people[user.ID] = true
		}
	}
	if !IsMultiUserMode() {
		people[GetDefaultUserID()] = true
	}
	seen := map[string]bool{}
	out := []projectVaultToMove{}
	for person := range people {
		if strings.TrimSpace(person) == "" {
			continue
		}
		data, err := vaultPersonRequest(ctx, person, http.MethodGet, "/api/vaults", nil)
		if err != nil {
			continue
		}
		var list struct {
			Vaults []struct {
				Group struct {
					ID, ProjectRoot string
				} `json:"group"`
				Role        string   `json:"role"`
				SecretNames []string `json:"secret_names"`
			} `json:"vaults"`
		}
		if json.Unmarshal(data, &list) != nil {
			continue
		}
		for _, row := range list.Vaults {
			root := row.Group.ProjectRoot
			if root == "" || root == brainSecretsPath || row.Role != "owner" || len(row.SecretNames) == 0 || seen[row.Group.ID] {
				continue
			}
			seen[row.Group.ID] = true
			out = append(out, projectVaultToMove{ID: row.Group.ID, Root: root, Holder: person, Names: append([]string{}, row.SecretNames...)})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Root < out[j].Root })
	return out
}

// backupProjectVaultValues writes the stored (encrypted) values of every name about to move, before anything changes,
// to <state>/vault-migration-backups/<UTC stamp>/values.json; each changed manifest is copied next to it.
func (api *StreamingAPI) backupProjectVaultValues(ctx context.Context, vaults []projectVaultToMove) (string, error) {
	state, err := workflowCLIStateRoot()
	if err != nil || strings.TrimSpace(state) == "" {
		return "", fmt.Errorf("no state folder for the migration backup")
	}
	dir := filepath.Join(state, "vault-migration-backups", time.Now().UTC().Format("20060102T150405Z"))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create migration backup: %w", err)
	}
	moving := map[string]bool{}
	for _, vault := range vaults {
		for _, name := range vault.Names {
			moving[name] = true
		}
	}
	rows, err := api.chatStore.ListUserSecrets(ctx, managedGlobalSecretsUserID)
	if err != nil {
		return "", fmt.Errorf("read stored values for the migration backup: %w", err)
	}
	values := map[string]string{}
	for _, row := range rows {
		if moving[row.Name] {
			values[row.Name] = row.EncryptedValue
		}
	}
	encoded, err := json.MarshalIndent(map[string]any{"values": values, "aad": "agentworks:global-secret:<name>"}, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "values.json"), encoded, 0o600); err != nil {
		return "", fmt.Errorf("write migration backup: %w", err)
	}
	log.Printf("[VAULT_MIGRATE] backup=%s projects=%d secrets=%d", dir, len(vaults), len(values))
	return dir, nil
}

// moveProjectVault moves one project vault's secrets; it reports whether to try again later.
func (api *StreamingAPI) moveProjectVault(ctx context.Context, vault projectVaultToMove, backup string) bool {
	owner := api.projectSecretsOwner(vault.Holder, vault.Root)
	skip := func(reason string, retry bool) bool {
		for _, full := range vault.Names {
			logVaultMove(vault.Root, vaultSecretShortName(full), "", "skipped", reason)
		}
		return retry
	}
	if !personVaultActive(owner) {
		return skip("owner_inactive", false)
	}
	personal, err := ensurePersonalVault(ctx, owner)
	if err != nil {
		return skip("personal_vault_unavailable", true)
	}
	manifest, err := readProjectSelectionManifest(ctx, vault.Root)
	if err != nil {
		return skip("manifest_unavailable", true)
	}
	label := projectVaultLabel(vault.Root, manifest)
	type moved struct{ name, full, result string }
	var done []moved
	retry := false
	for _, old := range vault.Names {
		name := vaultSecretShortName(old)
		managedGlobalsMu.RLock()
		value, ok := managedGlobals[old]
		managedGlobalsMu.RUnlock()
		if !ok || value == "" {
			logVaultMove(vault.Root, name, "", "skipped", "no_value")
			continue
		}
		target, result, err := api.placeInOwnerVault(ctx, owner, personal, name, label, value)
		if err != nil {
			logVaultMove(vault.Root, name, "", "skipped", "store_failed")
			retry = true
			continue
		}
		done = append(done, moved{name, target, result})
	}
	if len(done) == 0 {
		return retry
	}
	// The project selects the moved secret where it selected the old one, under the Vault name.
	if manifest.found {
		changed := false
		for _, item := range done {
			if manifest.swap(item.name, item.full) {
				changed = true
			}
		}
		if changed {
			if err := manifest.save(ctx, backup); err != nil {
				for _, item := range done {
					logVaultMove(vault.Root, item.name, vaultSecretShortName(item.full), "skipped", "manifest_write_failed")
				}
				return true
			}
		}
	}
	for _, item := range done {
		old, _ := vaultSecretName(vault.ID, item.name)
		if _, err := vaultPersonRequest(ctx, vault.Holder, http.MethodDelete, "/api/vaults/"+url.PathEscape(vault.ID)+"/secrets/"+url.PathEscape(old), nil); err != nil && !strings.Contains(err.Error(), "unknown secret") {
			logVaultMove(vault.Root, item.name, vaultSecretShortName(item.full), "skipped", "old_copy_not_removed")
			retry = true
			continue
		}
		if err := api.removeManagedGlobalSecretValue(ctx, old); err != nil && err != errGlobalNotFound {
			retry = true
		}
		if item.result == "renamed" {
			noteVaultRename(vault.Root, item.name, vaultSecretShortName(item.full))
		}
		logVaultMove(vault.Root, item.name, vaultSecretShortName(item.full), item.result, "")
	}
	markWorkflowSecretsChanged(vault.Root)
	return retry
}

// placeInOwnerVault puts value in the owner's My accounts: under NAME when it is free or already holds the same value,
// else under <PROJECT>_NAME (the project's name, upper-cased), so neither value replaces the other.
func (api *StreamingAPI) placeInOwnerVault(ctx context.Context, owner, personal, name, label, value string) (string, string, error) {
	candidates := []string{name}
	renamed := label + "_" + name
	if len(renamed) > 128 {
		renamed = renamed[:128]
	}
	candidates = append(candidates, renamed)
	for i := 2; i <= 9; i++ {
		suffix := fmt.Sprintf("_%d", i)
		base := renamed
		if len(base)+len(suffix) > 128 {
			base = base[:128-len(suffix)]
		}
		candidates = append(candidates, base+suffix)
	}
	for i, candidate := range candidates {
		full, err := vaultSecretName(personal, candidate)
		if err != nil {
			return "", "", err
		}
		managedGlobalsMu.RLock()
		existing, taken := managedGlobals[full]
		managedGlobalsMu.RUnlock()
		if taken && existing != value {
			continue
		}
		result := "moved"
		if i > 0 {
			result = "renamed"
		}
		if taken {
			result = "selected_existing"
		}
		// Registering again is harmless; it makes sure the owner's vault lists the name.
		if err := api.setVaultSecret(ctx, owner, personal, candidate, value, true); err != nil {
			return "", "", err
		}
		return full, result, nil
	}
	return "", "", fmt.Errorf("no free name")
}

// projectVaultLabel is the project's name for a renamed secret: its label (or folder), upper-cased, with every
// character other than A-Z, 0-9 and _ turned into _.
func projectVaultLabel(root string, manifest *projectSelectionManifest) string {
	label := ""
	if manifest != nil && manifest.doc != nil {
		label, _ = manifest.doc["label"].(string)
	}
	if strings.TrimSpace(label) == "" {
		label = path.Base(strings.TrimRight(root, "/"))
	}
	label = projectLabelUnsafe.ReplaceAllString(strings.ToUpper(strings.TrimSpace(label)), "_")
	if label == "" || label[0] >= '0' && label[0] <= '9' {
		label = "P_" + label
	}
	return label
}

func logVaultMove(project, name, to, result, reason string) {
	log.Printf("[VAULT_MIGRATE] project=%s secret=%s to=%s result=%s reason=%s", project, name, to, result, reason)
}

// projectSelectionManifest is the file that holds a project's selections: workflow.json (Crews, Code, Goals, Flows),
// or product.json for a product that keeps its settings there. found is false when the project has neither.
type projectSelectionManifest struct {
	file, raw string
	found     bool
	doc       map[string]any
}

func readProjectSelectionManifest(ctx context.Context, root string) (*projectSelectionManifest, error) {
	for _, name := range []string{"workflow.json", "product.json"} {
		file := path.Join(root, name)
		raw, found, err := readFileFromWorkspace(ctx, file)
		if err != nil {
			return nil, err
		}
		if !found {
			continue
		}
		var doc map[string]any
		if err := json.Unmarshal([]byte(raw), &doc); err != nil {
			return nil, err
		}
		return &projectSelectionManifest{file: file, raw: raw, found: true, doc: doc}, nil
	}
	return &projectSelectionManifest{}, nil
}

// swap moves name from selected_secrets to full in selected_global_secret_names, if the project selected it.
func (m *projectSelectionManifest) swap(name, full string) bool {
	capabilities, _ := m.doc["capabilities"].(map[string]any)
	if capabilities == nil {
		return false
	}
	project := manifestNames(capabilities["selected_secrets"])
	if !containsExactString(project, name) {
		return false
	}
	if rest := removeString(project, name); len(rest) > 0 {
		capabilities["selected_secrets"] = rest
	} else {
		delete(capabilities, "selected_secrets") // the retired list goes once nothing is left in it
	}
	capabilities["selected_global_secret_names"] = withProjectSecret(manifestNames(capabilities["selected_global_secret_names"]), full)
	return true
}

func (m *projectSelectionManifest) save(ctx context.Context, backup string) error {
	copyPath := filepath.Join(backup, "manifests", filepath.FromSlash(m.file))
	if err := os.MkdirAll(filepath.Dir(copyPath), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(copyPath, []byte(m.raw), 0o600); err != nil {
		return err
	}
	return m.write(ctx)
}

// write saves the edited manifest.
func (m *projectSelectionManifest) write(ctx context.Context) error {
	m.doc["updated_at"] = time.Now().UTC().Format(time.RFC3339)
	encoded, err := json.MarshalIndent(m.doc, "", "  ")
	if err != nil {
		return err
	}
	return writeFileToWorkspace(ctx, m.file, string(encoded)+"\n")
}

// manifestNames reads a list of names from a decoded manifest, or from one this code already edited.
func manifestNames(value any) []string {
	var items []any
	switch list := value.(type) {
	case []any:
		items = list
	case []string:
		for _, name := range list {
			items = append(items, name)
		}
	}
	names := []string{}
	for _, item := range items {
		if name, ok := item.(string); ok && strings.TrimSpace(name) != "" {
			names = appendUniqueStrings(names, strings.TrimSpace(name))
		}
	}
	return names
}

// A secret renamed when it moved (its name was taken in the owner's Vault by a different value) is listed in the
// project's Secrets panel, so the owner can update scripts that used $SECRET_<old name> (reviewer, 5 Nov).
type vaultRename struct {
	From string `json:"from"`
	To   string `json:"to"`
}

func vaultRenamesPath(root string) (string, error) {
	normalized, err := chathistory.NormalizeWorkflowSecretPath(root)
	if err != nil {
		return "", err
	}
	state, err := crewroot.StateRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(state, "vault-migration", "renamed", crewTransferDigest(normalized)+".json"), nil
}

func readVaultRenames(root string) []vaultRename {
	path, err := vaultRenamesPath(root)
	if err != nil {
		return nil
	}
	var out []vaultRename
	if raw, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(raw, &out)
	}
	return out
}

func noteVaultRename(root, from, to string) {
	path, err := vaultRenamesPath(root)
	if err != nil {
		return
	}
	list := readVaultRenames(root)
	if slices.ContainsFunc(list, func(r vaultRename) bool { return r.From == from && r.To == to }) {
		return
	}
	list = append(list, vaultRename{From: from, To: to})
	if err = os.MkdirAll(filepath.Dir(path), 0o700); err == nil {
		err = fsutil.WriteJSONAtomic(path, list, 0o600)
	}
	if err != nil {
		log.Printf("[VAULT_MIGRATE] project=%s secret=%s to=%s rename_note_failed=%v", root, from, to, err)
	}
}

// vaultRenameNotices are the Secrets panel's lines for the project's renamed secrets.
func vaultRenameNotices(root string) []string {
	out := []string{}
	for _, r := range readVaultRenames(root) {
		out = append(out, fmt.Sprintf("%s was renamed %s when it moved to your Vault (that name was already used). Scripts now use $SECRET_%s.", r.From, r.To, r.To))
	}
	return out
}
