package sparkquillproduct

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
)

// Legacy migration: ~/.sunlit-learning (the standalone family-server's home)
// into the platform family workspace <docsDir>/_users/<user>/Chats/SparkQuill.
//
// Copy, never move: the source is left byte-for-byte untouched, which is the
// whole safety story — there is no separate backup step because nothing is
// ever taken away from the family. Idempotent through a marker written only
// on full success; a non-empty target without that marker is refused unless
// the caller explicitly allows merging (never overwriting) into it.
// Everything that has no platform home is kept under _legacy/ rather than
// dropped. See docs/design/sparkquill_desktop_on_platform_plan.md A9/P5.

const LegacyMigrationMarker = ".migrated-from-sunlit"

// LegacyMigrationOptions configures MigrateLegacy.
type LegacyMigrationOptions struct {
	// SourceDir is the standalone app's home (default ~/.sunlit-learning):
	// family.json plus workspace/.
	SourceDir string
	// DocsDir is the platform workspace root ($WORKSPACE_DOCS_PATH).
	DocsDir string
	// UserID selects the per-user tree; empty means "default".
	UserID string
	// DryRun plans and reports without writing anything.
	DryRun bool
	// AllowExisting merges into a non-empty target (never overwriting);
	// otherwise a non-empty, unmarked target is refused.
	AllowExisting bool
	// KnownEngines are the parent profile's provider_options ids; a legacy
	// engine outside this set is dropped rather than carried over.
	KnownEngines []string
	// Log receives one line per decision; nil discards.
	Log func(format string, args ...interface{})
}

// LegacyMigrationReport summarizes what MigrateLegacy did (or would do).
type LegacyMigrationReport struct {
	Skipped     string   `json:"skipped,omitempty"`
	Activities  int      `json:"activities"`
	Archived    int      `json:"archived"`
	KeysMoved   int      `json:"keys_moved"`
	FilesCopied int      `json:"files_copied"`
	Legacy      int      `json:"legacy_files"`
	Warnings    []string `json:"warnings,omitempty"`
	Target      string   `json:"target"`
	Marker      string   `json:"marker,omitempty"`
}

// ErrTargetNotEmpty is returned when the family workspace already has content
// and AllowExisting is false.
var ErrTargetNotEmpty = errors.New("sparkquill workspace already has content and is not marked as migrated; refusing to write into it (pass --allow-existing to merge)")

// LegacyFamilyRoot is the family workspace for a user under docsDir.
func LegacyFamilyRoot(docsDir, userID string) string {
	if strings.TrimSpace(userID) == "" {
		userID = "default"
	}
	return filepath.Join(docsDir, workspaceref.UsersDir, userID, "Chats", "SparkQuill")
}

// DefaultLegacySourceDir is ~/.sunlit-learning, or SPARKQUILL_LEGACY_DIR.
func DefaultLegacySourceDir() string {
	if v := strings.TrimSpace(os.Getenv("SPARKQUILL_LEGACY_DIR")); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".sunlit-learning")
}

// legacyTopLevelSkip are workspace entries with no platform home and no
// value as evidence: caches, scratch, seeded skills the platform embeds,
// tool state, and the standalone app's own markers.
var legacyTopLevelSkip = map[string]bool{
	"skills": true, ".agents": true, ".codex": true, ".git": true, ".local": true, ".cache": true,
	".tmp": true, "tool_output_folder": true, "backup": true, "backup.json": true,
	"whatsapp-routing.json": true, ".migrated-v2": true, ".gitignore": true, ".DS_Store": true,
}

// legacyVerbatimDirs are copied as they are; the platform layout keeps them.
var legacyVerbatimDirs = []string{MaterialsFolder, InboxFolder, ReportsFolder, MemoryFolder}

type legacyMigration struct {
	opts   LegacyMigrationOptions
	src    string // workspace dir inside SourceDir
	dst    string // family root
	report LegacyMigrationReport
	// taken tracks keys/ destinations so two activities never claim one name.
	taken map[string]bool
	// slugFor maps old workspace-relative activity dirs to allocated slugs.
	slugFor map[string]string
	// allocated tracks activities/<slug> and archive/<slug> claimed this run.
	allocated map[string]bool
}

func (m *legacyMigration) logf(format string, args ...interface{}) {
	if m.opts.Log != nil {
		m.opts.Log(format, args...)
	}
}

func (m *legacyMigration) warn(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	m.report.Warnings = append(m.report.Warnings, msg)
	m.logf("WARN %s", msg)
}

// MigrateLegacy copies a standalone SparkQuill home into the platform family
// workspace. It is safe to call on every boot: it returns quickly with
// Skipped set when there is nothing to do.
func MigrateLegacy(opts LegacyMigrationOptions) (LegacyMigrationReport, error) {
	m := &legacyMigration{opts: opts, taken: map[string]bool{}, slugFor: map[string]string{}, allocated: map[string]bool{}}
	m.dst = LegacyFamilyRoot(opts.DocsDir, opts.UserID)
	m.report.Target = m.dst
	if strings.TrimSpace(opts.DocsDir) == "" {
		return m.report, errors.New("docs dir is required")
	}
	source := strings.TrimSpace(opts.SourceDir)
	if source == "" {
		source = DefaultLegacySourceDir()
	}
	m.src = filepath.Join(source, "workspace")

	marker := filepath.Join(m.dst, LegacyMigrationMarker)
	if _, err := os.Stat(marker); err == nil {
		m.report.Skipped = "already migrated (" + marker + ")"
		return m.report, nil
	}
	if info, err := os.Stat(source); err != nil || !info.IsDir() {
		m.report.Skipped = "no legacy home at " + source
		return m.report, nil
	}
	if _, err := os.Stat(filepath.Join(source, "family.json")); err != nil {
		if _, werr := os.Stat(m.src); werr != nil {
			m.report.Skipped = "legacy home has neither family.json nor workspace/"
			return m.report, nil
		}
	}
	if !opts.AllowExisting {
		if entries, err := os.ReadDir(m.dst); err == nil {
			for _, e := range entries {
				if e.Name() != LegacyMigrationMarker && e.Name() != ".DS_Store" {
					return m.report, fmt.Errorf("%w: %s", ErrTargetNotEmpty, m.dst)
				}
			}
		}
	}
	m.logf("migrating %s -> %s (dry_run=%v)", source, m.dst, opts.DryRun)
	m.seedTaken()

	if err := m.migrateFamilyFile(source); err != nil {
		return m.report, err
	}
	if err := m.migrateActivities(); err != nil {
		return m.report, err
	}
	if err := m.migrateArchive(); err != nil {
		return m.report, err
	}
	for _, dir := range legacyVerbatimDirs {
		if err := m.copyTree(filepath.Join(m.src, dir), filepath.Join(m.dst, dir), nil); err != nil {
			return m.report, fmt.Errorf("copy %s: %w", dir, err)
		}
	}
	if err := m.migrateConversations(); err != nil {
		return m.report, err
	}
	if err := m.migrateLeftovers(); err != nil {
		return m.report, err
	}
	if err := m.migrateCurrentActivity(); err != nil {
		return m.report, err
	}
	if opts.DryRun {
		m.logf("dry run: nothing written")
		return m.report, nil
	}
	payload, _ := json.MarshalIndent(map[string]interface{}{
		"source":      source,
		"migrated_at": time.Now().UTC().Format(time.RFC3339),
		"report":      m.report,
	}, "", "  ")
	if err := os.MkdirAll(m.dst, 0o700); err != nil {
		return m.report, err
	}
	if err := os.WriteFile(marker, append(payload, '\n'), 0o600); err != nil {
		return m.report, fmt.Errorf("write marker: %w", err)
	}
	m.report.Marker = marker
	m.logf("done: %d activities, %d archived, %d keys, %d files, %d legacy", m.report.Activities, m.report.Archived, m.report.KeysMoved, m.report.FilesCopied, m.report.Legacy)
	return m.report, nil
}

// seedTaken pre-populates key destinations from an existing keys/ folder so a
// merge into a live workspace never overwrites a key already there.
func (m *legacyMigration) seedTaken() {
	entries, err := os.ReadDir(filepath.Join(m.dst, KeysFolder))
	if err != nil {
		return
	}
	for _, e := range entries {
		m.taken[path.Join(KeysFolder, e.Name())] = true
	}
}

// --- family.json --------------------------------------------------------------

func (m *legacyMigration) migrateFamilyFile(source string) error {
	raw, err := os.ReadFile(filepath.Join(source, "family.json"))
	if err != nil {
		m.warn("no family.json in %s; the family will onboard again", source)
		return nil
	}
	var old map[string]interface{}
	if err := json.Unmarshal(raw, &old); err != nil {
		m.warn("family.json unreadable (%v); the family will onboard again", err)
		return nil
	}
	var existing FamilyState
	if b, err := os.ReadFile(filepath.Join(m.dst, FamilyFile)); err == nil {
		_ = json.Unmarshal(b, &existing)
	}
	next := existing
	if next.Engine == "" {
		if engine, _ := old["engine"].(string); engine != "" {
			if m.engineKnown(engine) {
				next.Engine = engine
			} else {
				m.warn("legacy engine %q is not offered by the platform profile; dropped", engine)
			}
		}
	}
	if next.Child == nil {
		if child, ok := old["child"].(map[string]interface{}); ok {
			name, _ := child["name"].(string)
			if strings.TrimSpace(name) != "" {
				grade, _ := child["grade"].(string)
				board, _ := child["board"].(string)
				lang, _ := child["language"].(string)
				next.Child = &Child{Name: name, Grade: grade, Board: board, Language: lang}
			}
		}
	}
	if next.ParentLabel == "" {
		next.ParentLabel, _ = old["parent_label"].(string)
	}
	if next.PinHash == "" {
		next.PinHash, _ = old["pin_hash"].(string)
	}
	if len(next.WatchSites) == 0 {
		next.WatchSites = legacyWatchSites(old)
	}
	for _, dropped := range []string{"fast_mode", "child_fast_mode", "selected_models", "schedule", "whatsapp_voice_enabled"} {
		if _, present := old[dropped]; present {
			m.logf("family.json: %s has no platform home yet; dropped", dropped)
		}
	}
	content, err := encodeJSON(next)
	if err != nil {
		return err
	}
	return m.writeFile(filepath.Join(m.dst, FamilyFile), []byte(content))
}

func (m *legacyMigration) engineKnown(engine string) bool {
	for _, k := range m.opts.KnownEngines {
		if strings.EqualFold(strings.TrimSpace(k), strings.TrimSpace(engine)) {
			return true
		}
	}
	return false
}

// legacyWatchSites folds pulse.watch_sites, the older pulse.school_portal_url
// and any top-level watch_sites into one de-duplicated list.
func legacyWatchSites(old map[string]interface{}) []string {
	var out []string
	seen := map[string]bool{}
	add := func(v interface{}) {
		s, _ := v.(string)
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			return
		}
		seen[s] = true
		out = append(out, s)
	}
	addList := func(v interface{}) {
		if list, ok := v.([]interface{}); ok {
			for _, item := range list {
				add(item)
			}
		}
	}
	if pulse, ok := old["pulse"].(map[string]interface{}); ok {
		addList(pulse["watch_sites"])
		add(pulse["school_portal_url"])
	}
	addList(old["watch_sites"])
	return out
}

// LegacyPulseEnabled reports whether the standalone app's check-in was on, so
// the platform schedule can be seeded to match (and not fire immediately).
func LegacyPulseEnabled(sourceDir string) (enabled bool, lastRunAt string) {
	raw, err := os.ReadFile(filepath.Join(sourceDir, "family.json"))
	if err != nil {
		return false, ""
	}
	var old struct {
		Pulse struct {
			Enabled   bool   `json:"enabled"`
			LastRunAt string `json:"last_run_at"`
		} `json:"pulse"`
	}
	if json.Unmarshal(raw, &old) != nil {
		return false, ""
	}
	return old.Pulse.Enabled, old.Pulse.LastRunAt
}

// --- activities ---------------------------------------------------------------

// isActivityDir reports whether dir holds an activity.json.
func isActivityDir(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, "activity.json"))
	return err == nil && !info.IsDir()
}

// findActivityDirs lists every activity folder under root (depth-first,
// sorted), never descending into an activity folder itself.
func findActivityDirs(root string) []string {
	var out []string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		if p != root && isActivityDir(p) {
			out = append(out, p)
			return filepath.SkipDir
		}
		return nil
	})
	sort.Strings(out)
	return out
}

// allocateSlug turns an old folder name into a free activities/ (or
// archive/) slug: lowercase, validated, numeric suffix on collision with
// anything already on disk or claimed this run.
func (m *legacyMigration) allocateSlug(folder, base string) (string, error) {
	slug := strings.ToLower(strings.TrimSpace(base))
	slug = strings.NewReplacer(" ", "-", "_", "-").Replace(slug)
	if !activitySlugPattern.MatchString(slug) {
		return "", fmt.Errorf("folder name %q cannot become an activity slug", base)
	}
	candidate := slug
	for i := 2; ; i++ {
		rel := path.Join(folder, candidate)
		if !m.allocated[rel] {
			if _, err := os.Stat(filepath.Join(m.dst, filepath.FromSlash(rel))); err != nil {
				m.allocated[rel] = true
				return candidate, nil
			}
		}
		candidate = fmt.Sprintf("%s-%d", slug, i)
	}
}

func (m *legacyMigration) migrateActivities() error {
	entries, err := os.ReadDir(m.src)
	if err != nil {
		return nil
	}
	skip := map[string]bool{ArchiveFolder: true, "_legacy": true, "conversations": true}
	for _, d := range legacyVerbatimDirs {
		skip[d] = true
	}
	for _, e := range entries {
		if !e.IsDir() || skip[e.Name()] || legacyTopLevelSkip[e.Name()] || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		subject := filepath.Join(m.src, e.Name())
		for _, dir := range findActivityDirs(subject) {
			if err := m.copyActivity(dir, ActivitiesFolder, true); err != nil {
				return err
			}
		}
		// Anything left in the subject tree that is not inside an activity
		// is evidence with no home: keep it under _legacy/.
		if err := m.copyTree(subject, filepath.Join(m.dst, "_legacy", e.Name()), func(p string, d fs.DirEntry) bool {
			return d.IsDir() && isActivityDir(p)
		}); err != nil {
			return err
		}
	}
	return nil
}

func (m *legacyMigration) migrateArchive() error {
	root := filepath.Join(m.src, ArchiveFolder)
	if _, err := os.Stat(root); err != nil {
		return nil
	}
	for _, dir := range findActivityDirs(root) {
		if err := m.copyActivity(dir, ArchiveFolder, false); err != nil {
			return err
		}
	}
	return m.copyTree(root, filepath.Join(m.dst, "_legacy", ArchiveFolder), func(p string, d fs.DirEntry) bool {
		return d.IsDir() && isActivityDir(p)
	})
}

// copyActivity copies one activity folder to <folder>/<slug>: items and
// attempts/ verbatim, answer keys out to keys/, the child's conversation
// kept beside as legacy-conversation.json, CLI session handles dropped, and
// product.json synthesized when missing so the child profile can bind a
// conversation to it (only for live activities).
func (m *legacyMigration) copyActivity(dir, folder string, live bool) error {
	oldRel, _ := filepath.Rel(m.src, dir)
	oldRel = filepath.ToSlash(oldRel)
	slug, err := m.allocateSlug(folder, filepath.Base(dir))
	if err != nil {
		m.warn("%s: %v; kept under _legacy/", oldRel, err)
		return m.copyTree(dir, filepath.Join(m.dst, "_legacy", filepath.FromSlash(oldRel)), nil)
	}
	newRel := path.Join(folder, slug)
	newDir := filepath.Join(m.dst, filepath.FromSlash(newRel))
	m.slugFor[oldRel] = newRel

	var manifest ActivityManifest
	if b, err := os.ReadFile(filepath.Join(dir, "activity.json")); err == nil {
		_ = json.Unmarshal(b, &manifest)
	}
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || p == dir {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		name := d.Name()
		if d.IsDir() {
			return nil
		}
		switch {
		case strings.HasSuffix(name, ".session.json"):
			m.logf("%s: dropped CLI session handle %s", oldRel, rel)
			return nil
		case rel == "conversation.json":
			return m.copyFile(p, filepath.Join(newDir, "legacy-conversation.json"))
		case !strings.Contains(rel, "/") && isAnswerKey(name):
			dest := answerKeyDestination(slug, name, m.taken)
			m.report.KeysMoved++
			m.logf("%s: key %s -> %s", oldRel, name, dest)
			return m.copyFile(p, filepath.Join(m.dst, filepath.FromSlash(dest)))
		}
		return m.copyFile(p, filepath.Join(newDir, filepath.FromSlash(rel)))
	})
	if err != nil {
		return err
	}
	if live {
		if _, err := os.Stat(filepath.Join(dir, "product.json")); err != nil {
			title := strings.TrimSpace(manifest.Title)
			if title == "" {
				title = slug
			}
			project := activityProject{SchemaVersion: 1, Product: ChildProfileID, ID: slug, Title: title, Description: manifest.Goal, SessionID: "product-" + uuid.NewString()}
			content, _ := encodeJSON(project)
			if err := m.writeFile(filepath.Join(newDir, "product.json"), []byte(content)); err != nil {
				return err
			}
		}
		m.report.Activities++
	} else {
		m.report.Archived++
	}
	m.logf("%s -> %s", oldRel, newRel)
	return nil
}

// --- conversations, leftovers, pointer ----------------------------------------

func (m *legacyMigration) migrateConversations() error {
	root := filepath.Join(m.src, "conversations")
	if _, err := os.Stat(root); err != nil {
		return nil
	}
	return m.copyTree(root, filepath.Join(m.dst, "_legacy", "conversations"), func(p string, d fs.DirEntry) bool {
		return !d.IsDir() && strings.HasSuffix(d.Name(), ".session.json")
	})
}

func (m *legacyMigration) migrateLeftovers() error {
	entries, err := os.ReadDir(m.src)
	if err != nil {
		return nil
	}
	handled := map[string]bool{ArchiveFolder: true, "conversations": true, "current-activity.json": true, "_legacy": true}
	for _, d := range legacyVerbatimDirs {
		handled[d] = true
	}
	if legacy := filepath.Join(m.src, "_legacy"); dirExistsAt(legacy) {
		if err := m.copyTree(legacy, filepath.Join(m.dst, "_legacy"), nil); err != nil {
			return err
		}
	}
	for _, e := range entries {
		name := e.Name()
		if handled[name] || e.IsDir() && !legacyTopLevelSkip[name] && !strings.HasPrefix(name, ".") {
			continue // subject dirs were consumed by migrateActivities
		}
		if legacyTopLevelSkip[name] || strings.HasPrefix(name, ".") {
			m.logf("skipped %s (no platform home)", name)
			continue
		}
		dst := filepath.Join(m.dst, "_legacy", name)
		if e.IsDir() {
			if err := m.copyTree(filepath.Join(m.src, name), dst, nil); err != nil {
				return err
			}
			continue
		}
		m.report.Legacy++
		m.logf("%s has no platform home; kept under _legacy/", name)
		if err := m.copyFile(filepath.Join(m.src, name), dst); err != nil {
			return err
		}
	}
	return nil
}

func (m *legacyMigration) migrateCurrentActivity() error {
	raw, err := os.ReadFile(filepath.Join(m.src, "current-activity.json"))
	if err != nil {
		return nil
	}
	var pointer struct {
		Dir string `json:"dir"`
	}
	if json.Unmarshal(raw, &pointer) != nil || strings.TrimSpace(pointer.Dir) == "" {
		return nil
	}
	old := strings.Trim(filepath.ToSlash(pointer.Dir), "/")
	newRel, ok := m.slugFor[old]
	if !ok {
		m.warn("current-activity.json points at %q, which was not migrated; the parent will hand off again", old)
		return nil
	}
	content, _ := encodeJSON(map[string]string{"dir": newRel})
	m.logf("current-activity.json -> %s", newRel)
	return m.writeFile(filepath.Join(m.dst, "current-activity.json"), []byte(content))
}

// --- filesystem primitives (copy-only, never overwrite) -------------------------

func dirExistsAt(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

// copyTree copies src into dst, skipping entries for which skip returns
// true (and, for directories, their subtrees).
func (m *legacyMigration) copyTree(src, dst string, skip func(p string, d fs.DirEntry) bool) error {
	if !dirExistsAt(src) {
		return nil
	}
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if p == src {
			return nil
		}
		if skip != nil && skip(p, d) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(src, p)
		if strings.Contains(dst, filepath.Join(m.dst, "_legacy")) {
			m.report.Legacy++
		}
		return m.copyFile(p, filepath.Join(dst, rel))
	})
}

// copyFile copies one regular file, never overwriting: a name already at
// dst gets the incoming file beside it with a .migrated-dup suffix.
func (m *legacyMigration) copyFile(src, dst string) error {
	if _, err := os.Lstat(dst); err == nil {
		alt := dst + ".migrated-dup"
		for i := 2; ; i++ {
			if _, err := os.Lstat(alt); err != nil {
				break
			}
			alt = fmt.Sprintf("%s.migrated-dup%d", dst, i)
		}
		m.logf("name collision at %s — kept incoming as %s", dst, filepath.Base(alt))
		dst = alt
	}
	m.report.FilesCopied++
	if m.opts.DryRun {
		return nil
	}
	data, err := os.ReadFile(src) //nolint:gosec // G304: paths come from walking the family's own legacy workspace.
	if err != nil {
		return fmt.Errorf("read %s: %w", src, err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o600)
}

// writeFile writes generated content (family.json, product.json, pointer),
// which may replace an earlier generated version but never a family file it
// did not itself produce: callers only pass paths whose existing content
// was read and merged first.
func (m *legacyMigration) writeFile(dst string, data []byte) error {
	m.report.FilesCopied++
	if m.opts.DryRun {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o600)
}
