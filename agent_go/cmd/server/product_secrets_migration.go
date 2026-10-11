package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/chathistory"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
)

type productSecretsMigrationOptions struct {
	Product string
	DocsDir string
	User    string
	Apply   bool
	Log     func(format string, args ...interface{})
}

type productSecretsBoxReport struct {
	Box            string   `json:"box"`
	Migrated       []string `json:"migrated"`
	AlreadyPresent []string `json:"already_present"`
	Manifest       string   `json:"manifest"`
	ManifestMade   bool     `json:"manifest_created"`
}

type productSecretsUserReport struct {
	User     string                    `json:"user"`
	Personal int                       `json:"personal_secrets"`
	Boxes    []productSecretsBoxReport `json:"boxes"`
	Archived bool                      `json:"archived"`
	Skipped  string                    `json:"skipped,omitempty"`
	Errors   []string                  `json:"errors,omitempty"`
}

type productSecretsMigrationReport struct {
	Product string                     `json:"product"`
	DocsDir string                     `json:"docs_dir"`
	Apply   bool                       `json:"applied"`
	Users   []productSecretsUserReport `json:"users"`
}

var migrateProductSecretsCmd = &cobra.Command{
	Use:   "migrate-product-secrets",
	Short: "Move retired personal secrets into a product's secret box",
	Long: `Moves each user's retired personal ("yours") secrets into the product's secret box: the fixed Chats/SparkQuill box for --product sparkquill, or every project box under Chats/Video Studio/projects for --product video-studio.

Values are decrypted with the storing user's key and re-encrypted for the box; names already in the box are never overwritten. Migrated names are attached to the box manifest's selected_secrets so runs keep resolving them. A user's personal file is archived (renamed, never deleted) only after every one of its secrets migrated cleanly. Users whose secrets cannot be decrypted, or who have secrets but no target boxes, are reported and left in place for a manual follow-up; only operational failures (bad flags, unreadable docs, missing AUTH_SECRET, write errors) fail the command.

Defaults to a dry-run report; pass --apply to write. Reads AUTH_SECRET and --docs-dir (WORKSPACE_DOCS_PATH) from the environment, like the server itself. Safe to re-run: migrated names are skipped as already present.`,
	RunE: runMigrateProductSecrets,
}

func init() {
	migrateProductSecretsCmd.Flags().String("product", "", "Product box to migrate into: sparkquill or video-studio (required)")
	migrateProductSecretsCmd.Flags().String("docs-dir", os.Getenv("WORKSPACE_DOCS_PATH"), "Workspace docs root (defaults to WORKSPACE_DOCS_PATH)")
	migrateProductSecretsCmd.Flags().String("user", "", "Migrate a single user directory only (defaults to all users)")
	migrateProductSecretsCmd.Flags().Bool("apply", false, "Write boxes, manifests, and archives (default is a dry-run report)")
}

func runMigrateProductSecrets(cmd *cobra.Command, _ []string) error {
	product, _ := cmd.Flags().GetString("product")
	docsDir, _ := cmd.Flags().GetString("docs-dir")
	user, _ := cmd.Flags().GetString("user")
	apply, _ := cmd.Flags().GetBool("apply")
	report, err := runProductSecretsMigration(productSecretsMigrationOptions{
		Product: strings.TrimSpace(product),
		DocsDir: strings.TrimSpace(docsDir),
		User:    strings.TrimSpace(user),
		Apply:   apply,
		Log:     func(format string, args ...interface{}) { fmt.Fprintf(cmd.ErrOrStderr(), format+"\n", args...) },
	})
	if err != nil {
		return err
	}
	out, _ := json.MarshalIndent(report, "", "  ")
	fmt.Fprintln(cmd.OutOrStdout(), string(out))
	return nil
}

func runProductSecretsMigration(opts productSecretsMigrationOptions) (*productSecretsMigrationReport, error) {
	log := opts.Log
	if log == nil {
		log = func(string, ...interface{}) {}
	}
	if opts.Product != "sparkquill" && opts.Product != "video-studio" {
		return nil, fmt.Errorf("--product is required (sparkquill or video-studio)")
	}
	if opts.DocsDir == "" {
		return nil, fmt.Errorf("--docs-dir (or WORKSPACE_DOCS_PATH) is required")
	}
	if err := ValidateConfiguredAuthSecret(); err != nil {
		return nil, err
	}
	store, err := chathistory.NewFilesystemStore(opts.DocsDir)
	if err != nil {
		return nil, err
	}
	users, err := productSecretsMigrationUsers(opts.DocsDir, opts.User)
	if err != nil {
		return nil, err
	}
	report := &productSecretsMigrationReport{Product: opts.Product, DocsDir: opts.DocsDir, Apply: opts.Apply, Users: []productSecretsUserReport{}}
	for _, userDir := range users {
		userReport, err := migrateProductSecretsUser(context.Background(), store, opts, userDir)
		if err != nil {
			return nil, err
		}
		report.Users = append(report.Users, *userReport)
	}
	if !opts.Apply {
		log("dry-run: no boxes, manifests, or archives were written; re-run with --apply to migrate")
	}
	return report, nil
}

// productSecretsMigrationUsers lists the _users directories holding real
// users. The reserved pseudo-users back live features (the shared boxes and
// managed globals) and are never migration sources.
func productSecretsMigrationUsers(docsDir, only string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(docsDir, workspaceref.UsersDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("list workspace users: %w", err)
	}
	var users []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if name == chathistory.SharedWorkflowSecretsUserID || name == managedGlobalSecretsUserID {
			continue
		}
		if only != "" && name != only {
			continue
		}
		users = append(users, name)
	}
	sort.Strings(users)
	if only != "" && len(users) == 0 {
		return nil, fmt.Errorf("user %q not found under _users", only)
	}
	return users, nil
}

func migrateProductSecretsUser(ctx context.Context, store chathistory.Store, opts productSecretsMigrationOptions, userDir string) (*productSecretsUserReport, error) {
	log := opts.Log
	if log == nil {
		log = func(string, ...interface{}) {}
	}
	report := &productSecretsUserReport{Boxes: []productSecretsBoxReport{}}
	report.User = userDir
	personal, err := store.ListUserSecrets(ctx, userDir)
	if err != nil {
		return nil, fmt.Errorf("list personal secrets of %s: %w", userDir, err)
	}
	report.Personal = len(personal)
	if len(personal) == 0 {
		return report, nil
	}
	// The directory name is the sanitized user id the store itself wrote;
	// for every id the store accepts it round-trips, so it is also the
	// decryption identity. Anything else fails loud below and stays put.
	plaintext := make(map[string]string, len(personal))
	for _, secret := range personal {
		value, err := decryptSecretValue(secret.EncryptedValue, userDir)
		if err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("%s: cannot decrypt personal copy (stored under a different AUTH_SECRET or corrupt); left in place", secret.Name))
			continue
		}
		plaintext[secret.Name] = value
	}
	if len(report.Errors) > 0 {
		log("user %s: %d of %d personal secrets unreadable; nothing was written for this user", userDir, len(report.Errors), len(personal))
		return report, nil
	}
	targets, err := productSecretsMigrationTargets(opts.DocsDir, opts.Product, userDir)
	if err != nil {
		return nil, err
	}
	if len(targets) == 0 {
		report.Skipped = "personal secrets present but no target boxes (video-studio projects appear here once created); left in place for a later run"
		log("user %s: %s", userDir, report.Skipped)
		return report, nil
	}
	names := make([]string, 0, len(plaintext))
	for name := range plaintext {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, target := range targets {
		boxReport, err := migrateProductSecretsBox(ctx, store, opts, userDir, target, plaintext)
		if err != nil {
			return nil, err
		}
		report.Boxes = append(report.Boxes, *boxReport)
	}
	if !opts.Apply {
		return report, nil
	}
	if err := archiveProductSecretsPersonalFile(opts.DocsDir, userDir); err != nil {
		return nil, err
	}
	report.Archived = true
	log("user %s: migrated %d personal secrets into %d box(es); personal file archived", userDir, len(names), len(targets))
	return report, nil
}

type productSecretsMigrationTarget struct {
	// Box is the canonical runtime path the secret tools and the Secrets
	// pane resolve, e.g. _users/<id>/Chats/SparkQuill.
	Box string
	// ManifestID seeds a created product.json only; existing manifests are
	// merged, never rewritten.
	ManifestID string
}

func productSecretsMigrationTargets(docsDir, product, userDir string) ([]productSecretsMigrationTarget, error) {
	public := []productSecretsMigrationTarget{}
	if product == "sparkquill" {
		public = append(public, productSecretsMigrationTarget{Box: "Chats/SparkQuill", ManifestID: "sparkquill"})
	} else {
		projectsRoot := filepath.Join(docsDir, workspaceref.UsersDir, userDir, "Chats", "Video Studio", "projects")
		projects, err := os.ReadDir(projectsRoot)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, nil
			}
			return nil, fmt.Errorf("list video-studio projects of %s: %w", userDir, err)
		}
		for _, project := range projects {
			if !project.IsDir() {
				continue
			}
			public = append(public, productSecretsMigrationTarget{
				Box:        "Chats/Video Studio/projects/" + project.Name(),
				ManifestID: project.Name(),
			})
		}
	}
	targets := make([]productSecretsMigrationTarget, 0, len(public))
	for _, target := range public {
		clean, err := cleanAgentProfileWorkspace(target.Box, userDir)
		if err != nil {
			return nil, fmt.Errorf("canonicalize box %q: %w", target.Box, err)
		}
		target.Box = agentProfileRuntimeWorkspace(userDir, clean)
		targets = append(targets, target)
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].Box < targets[j].Box })
	return targets, nil
}

func migrateProductSecretsBox(ctx context.Context, store chathistory.Store, opts productSecretsMigrationOptions, userDir string, target productSecretsMigrationTarget, plaintext map[string]string) (*productSecretsBoxReport, error) {
	report := &productSecretsBoxReport{Box: target.Box, Migrated: []string{}, AlreadyPresent: []string{}}
	existing, err := store.ListWorkflowSecrets(ctx, chathistory.SharedWorkflowSecretsUserID, target.Box)
	if err != nil {
		return nil, fmt.Errorf("list box %s: %w", target.Box, err)
	}
	present := make(map[string]bool, len(existing))
	for _, secret := range existing {
		present[secret.Name] = true
	}
	aad, err := sharedWorkflowSecretAAD(target.Box)
	if err != nil {
		return nil, fmt.Errorf("box %s: %w", target.Box, err)
	}
	names := make([]string, 0, len(plaintext))
	for name := range plaintext {
		names = append(names, name)
	}
	sort.Strings(names)
	attached := make([]string, 0, len(names))
	for _, name := range names {
		if present[name] {
			report.AlreadyPresent = append(report.AlreadyPresent, name)
			attached = append(attached, name)
			continue
		}
		if !opts.Apply {
			report.Migrated = append(report.Migrated, name)
			attached = append(attached, name)
			continue
		}
		encrypted, err := encryptSecretValueWithAAD(plaintext[name], aad)
		if err != nil {
			return nil, fmt.Errorf("encrypt %s for box %s: %w", name, target.Box, err)
		}
		if err := store.UpsertWorkflowSecret(ctx, chathistory.SharedWorkflowSecretsUserID, target.Box, name, encrypted); err != nil {
			return nil, fmt.Errorf("store %s in box %s: %w", name, target.Box, err)
		}
		report.Migrated = append(report.Migrated, name)
		attached = append(attached, name)
	}
	manifestPath, created, err := attachProductSecretsManifest(opts.DocsDir, target, attached, opts.Apply)
	if err != nil {
		return nil, err
	}
	report.Manifest = manifestPath
	report.ManifestMade = created
	return report, nil
}

// attachProductSecretsManifest merges names into the box manifest's
// capabilities.selected_secrets, creating the minimal manifest a
// fixed-workspace product expects when none exists yet. It mirrors
// updateProductSelectedSecrets without needing a workspace session.
func attachProductSecretsManifest(docsDir string, target productSecretsMigrationTarget, names []string, apply bool) (string, bool, error) {
	manifestPath := filepath.ToSlash(filepath.Join(target.Box, "product.json"))
	abs := filepath.Join(docsDir, filepath.FromSlash(manifestPath))
	raw, err := os.ReadFile(abs)
	created := false
	var manifest map[string]interface{}
	if err != nil {
		if !os.IsNotExist(err) {
			return "", false, fmt.Errorf("read %s: %w", manifestPath, err)
		}
		now := time.Now().UTC().Format(time.RFC3339)
		manifest = map[string]interface{}{
			"schema_version": 1,
			"id":             target.ManifestID,
			"capabilities":   map[string]interface{}{},
			"created_at":     now,
			"updated_at":     now,
		}
		created = true
	} else if err := json.Unmarshal(raw, &manifest); err != nil {
		return "", false, fmt.Errorf("decode %s: %w", manifestPath, err)
	}
	if manifest == nil {
		manifest = map[string]interface{}{}
	}
	capabilities, _ := manifest["capabilities"].(map[string]interface{})
	if capabilities == nil {
		capabilities = map[string]interface{}{}
	}
	current := []string{}
	if items, ok := capabilities["selected_secrets"].([]interface{}); ok {
		for _, item := range items {
			if name, ok := item.(string); ok {
				current = appendUniqueStrings(current, strings.TrimSpace(name))
			}
		}
	}
	for _, name := range names {
		current = appendUniqueStrings(current, name)
	}
	sort.Strings(current)
	capabilities["selected_secrets"] = current
	manifest["capabilities"] = capabilities
	manifest["updated_at"] = time.Now().UTC().Format(time.RFC3339)
	if !apply {
		return manifestPath, created, nil
	}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", false, fmt.Errorf("encode %s: %w", manifestPath, err)
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
		return "", false, fmt.Errorf("create %s: %w", filepath.Dir(manifestPath), err)
	}
	if err := os.WriteFile(abs, append(encoded, '\n'), 0644); err != nil {
		return "", false, fmt.Errorf("write %s: %w", manifestPath, err)
	}
	return manifestPath, created, nil
}

// archiveProductSecretsPersonalFile renames a fully migrated personal store
// aside. The bytes stay on disk for manual recovery; the store only ever
// reads the un-suffixed name, so the archive is invisible to the runtime.
func archiveProductSecretsPersonalFile(docsDir, userDir string) error {
	source := filepath.Join(docsDir, workspaceref.UsersDir, userDir, "secrets.json")
	stamp := time.Now().UTC().Format("20060102T150405Z")
	target := filepath.Join(docsDir, workspaceref.UsersDir, userDir, "secrets.json.migrated-"+stamp)
	if err := os.Rename(source, target); err != nil {
		return fmt.Errorf("archive personal secrets of %s: %w", userDir, err)
	}
	return nil
}
