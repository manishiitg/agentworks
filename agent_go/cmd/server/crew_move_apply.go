package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/cliruntime"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
)

// forgetCrewLocationCaches drops the process-wide crew alias and owner caches (after the registry changed under them).
func forgetCrewLocationCaches() {
	crewPathAliases.mu.Lock()
	crewPathAliases.aliases = nil
	crewPathAliases.mu.Unlock()
	crewOwners.mu.Lock()
	crewOwners.entries = map[string]crewOwnerEntry{}
	crewOwners.mu.Unlock()
}

// crewMover carries one run.
type crewMover struct {
	opts      *crewMoveOptions
	docs      *os.Root
	docsAbs   string // the docs root, symlinks resolved
	registry  *projectOwnerRegistry
	report    *crewMoveReport
	backupRun string
}

// runCrewMove is the whole command. The returned error is for operational failures and, in apply mode, for a run
// that left Crews blocked or failed (errCrewMoveIncomplete); everything per Crew is in the report.
func runCrewMove(ctx context.Context, opts crewMoveOptions) (*crewMoveReport, error) {
	if opts.Out == nil {
		opts.Out = os.Stderr
	}
	docsAbs, err := filepath.Abs(strings.TrimSpace(opts.DocsRoot))
	if err != nil || strings.TrimSpace(opts.DocsRoot) == "" {
		return nil, fmt.Errorf("--docs-root (or WORKSPACE_DOCS_PATH) is required")
	}
	if strings.TrimSpace(opts.StateRoot) == "" {
		return nil, fmt.Errorf("--state-root (or AGENTWORKS_STATE_ROOT) is required")
	}
	stateRoot, err := filepath.Abs(opts.StateRoot)
	if err != nil {
		return nil, err
	}
	opts.StateRoot = stateRoot
	resolved, err := filepath.EvalSymlinks(docsAbs)
	if err != nil {
		return nil, fmt.Errorf("docs root: %w", err)
	}
	docs, err := os.OpenRoot(docsAbs)
	if err != nil {
		return nil, fmt.Errorf("docs root: %w", err)
	}
	defer docs.Close()

	registry := projectOwnersAt(stateRoot)
	// The access assertions and the alias map run in this process: point them at this state area.
	previous := defaultProjectOwnersOverride
	defaultProjectOwnersOverride = registry
	forgetCrewLocationCaches()
	defer func() { defaultProjectOwnersOverride = previous; forgetCrewLocationCaches() }()

	// One move at a time on a host (the server is kept out by the per-Crew marker; two commands by this lock).
	if opts.Apply || opts.Rollback != "" || opts.Finalize {
		unlock, err := lockCrewMove(stateRoot)
		if err != nil {
			return nil, err
		}
		defer unlock()
	}

	mover := &crewMover{opts: &opts, docs: docs, docsAbs: resolved, registry: registry,
		report: &crewMoveReport{DocsRoot: docsAbs, StateRoot: stateRoot, Failed: map[string]string{}}}

	switch {
	case opts.Rollback != "":
		mover.report.Mode = "rollback"
		return mover.report, mover.rollback(ctx, opts.Rollback)
	case opts.Finalize:
		mover.report.Mode = "finalize"
		return mover.report, mover.finalize()
	}
	mover.report.Mode = "dry-run"
	if opts.Apply {
		mover.report.Mode = "apply"
	}
	return mover.report, mover.plan(ctx)
}

// plan discovers, qualifies and (with --apply) moves.
func (m *crewMover) plan(ctx context.Context) error {
	opts := m.opts
	cands, problems, err := discoverCrews(m.docs, m.registry, opts)
	if err != nil {
		return err
	}
	m.report.Problems = append(m.report.Problems, problems...)
	journals, err := listCrewMoveJournals(opts.StateRoot)
	if err != nil {
		return err
	}
	journalOf := map[string]*crewMoveJournal{}
	for _, j := range journals {
		journalOf[j.Folder] = j
	}
	readers := m.readers()
	// What is live in each Crew (the probe is also how the dry run shows what would block).
	crews := map[string]string{}
	for _, c := range cands {
		crews[c.plan.Folder] = filepath.Join(m.docsAbs, filepath.FromSlash(c.plan.Source))
	}
	probe := opts.Probe
	if probe == nil {
		probe = defaultCrewActivityProbe
	}
	live, probeErr := probe(ctx, opts.StateRoot, crews)
	if probeErr != nil {
		m.report.Problems = append(m.report.Problems, "activity probe failed: "+probeErr.Error())
	}
	runtimes := crewRuntimeFolders(opts.StateRoot, crews)
	var refs map[string][]crewReferenceHit
	if !opts.SkipReferenceScan {
		folders := make([]string, 0, len(cands))
		for _, c := range cands {
			folders = append(folders, c.plan.Folder)
		}
		refs = scanCrewReferences(m.docs, folders)
	}
	for i := range cands {
		plan := &cands[i].plan
		plan.Readers = readers
		plan.RuntimeFolders = len(runtimes[plan.Folder])
		plan.References = refs[plan.Folder]
		for _, what := range live[plan.Folder] {
			plan.Blockers = append(plan.Blockers, "[ACTIVE] "+what+": stop it (and wait for any turn to finish) before moving this Crew")
		}
		if j := journalOf[plan.Folder]; j != nil {
			plan.State = string(j.State)
		}
		// The Crew's collision check: Crew/<folder> may exist only as this Crew's own half-finished move.
		if _, err := m.docs.Lstat(plan.Dest); err == nil && journalOf[plan.Folder] == nil {
			plan.Blockers = append(plan.Blockers, fmt.Sprintf("%s already exists: nothing is merged; resolve the collision by hand", plan.Dest))
		}
		m.report.Crews = append(m.report.Crews, *plan)
	}
	// Journals whose Crew has left its owner's tree (switched, done, rolled back) are reported too.
	for _, j := range journals {
		if _, discovered := crews[j.Folder]; discovered || !selectedCrew(crewMovePlan{Folder: j.Folder, ProjectID: j.ProjectID}, opts.Crews) {
			continue
		}
		m.report.Crews = append(m.report.Crews, crewMovePlan{
			Folder: j.Folder, Owner: j.Owner, ProjectID: j.ProjectID, Source: j.Source, Dest: j.Dest, Files: j.Tree.Files, Dirs: j.Tree.Dirs,
			Symlinks: j.Tree.Symlinks, Bytes: j.Tree.Bytes, RegistryState: "shared", State: string(j.State),
		})
	}
	if !opts.Apply {
		return nil
	}
	return m.apply(ctx, cands, journalOf)
}

func (m *crewMover) readers() []string {
	if m.opts.Readers != nil {
		return m.opts.Readers()
	}
	defer func() { _ = recover() }()
	return usersWithProduct("work")
}

// apply moves what is selected and unblocked, finishing half-done moves first.
func (m *crewMover) apply(ctx context.Context, cands []crewMoveCandidate, journalOf map[string]*crewMoveJournal) error {
	opts := m.opts
	if strings.TrimSpace(opts.BackupDir) == "" {
		return fmt.Errorf("--apply needs --backup-dir: nothing is moved without a verified backup")
	}
	// Half-done moves (a crash) first: they are the state the server must not be left in.
	var pending []*crewMoveJournal
	for _, j := range journalOf {
		if j.State != crewMoveDone && j.State != crewMoveRolledBack && selectedCrew(crewMovePlan{Folder: j.Folder, ProjectID: j.ProjectID}, opts.Crews) {
			pending = append(pending, j)
		}
	}
	sort.Slice(pending, func(i, k int) bool { return pending[i].Folder < pending[k].Folder })
	var todo []crewMoveCandidate
	blocked := 0
	for _, c := range cands {
		if !selectedCrew(c.plan, opts.Crews) {
			continue
		}
		if j := journalOf[c.plan.Folder]; j != nil && j.State != crewMoveRolledBack && j.State != crewMoveDone {
			continue // resumed above
		}
		if len(c.plan.Blockers) > 0 {
			blocked++
			m.report.Skipped = append(m.report.Skipped, c.plan.Folder)
			continue
		}
		todo = append(todo, c)
	}
	if len(pending) == 0 && len(todo) == 0 {
		if blocked > 0 {
			return errCrewMoveIncomplete
		}
		return nil
	}

	// The backup of exactly what will be touched, verified readable before anything moves.
	if len(todo) > 0 {
		if err := m.makeBackup(todo); err != nil {
			return fmt.Errorf("backup failed, nothing was changed: %w", err)
		}
	}
	// Space for the copies: the old folders stay as tombstones until --finalize.
	if err := m.checkDocsSpace(todo); err != nil {
		return fmt.Errorf("not enough space on the docs volume, nothing was changed: %w", err)
	}

	for _, j := range pending {
		if err := m.moveOne(ctx, j, nil); err != nil {
			m.report.Failed[j.Folder] = err.Error()
			return errCrewMoveIncomplete
		}
		m.report.Resumed = append(m.report.Resumed, j.Folder)
	}
	for _, c := range todo {
		now := m.opts.now().Format(time.RFC3339)
		j := &crewMoveJournal{
			Folder: c.plan.Folder, Owner: c.plan.Owner, ProjectID: c.plan.ProjectID,
			Source: c.plan.Source, Staging: crewStagingRel(c.plan.Folder), Dest: c.plan.Dest, Tombstone: crewTombstoneRel(c.plan.Owner, c.plan.Folder),
			OldAbs: filepath.Join(m.docsAbs, filepath.FromSlash(c.plan.Source)), NewAbs: filepath.Join(m.docsAbs, filepath.FromSlash(c.plan.Dest)),
			State: crewMovePlanned, RootMode: uint32(c.rootMode), RootGID: c.rootGID, BackupRun: m.backupRun, StartedAt: now,
		}
		if err := j.save(opts.StateRoot, opts.now()); err != nil {
			return err
		}
		if err := m.moveOne(ctx, j, &c); err != nil {
			m.report.Failed[c.plan.Folder] = err.Error()
			return errCrewMoveIncomplete
		}
		m.report.Moved = append(m.report.Moved, c.plan.Folder)
	}
	// The final pass over everything this run moved.
	for _, folder := range append(append([]string(nil), m.report.Resumed...), m.report.Moved...) {
		j, err := loadCrewMoveJournal(opts.StateRoot, folder)
		if err != nil || j == nil {
			return fmt.Errorf("journal of %s: %w", folder, errors.Join(err, errCrewMoveIncomplete))
		}
		if err := m.verifyMoved(j); err != nil {
			m.report.Failed[folder] = "final verification: " + err.Error()
			return errCrewMoveIncomplete
		}
		m.report.Verified = append(m.report.Verified, folder)
	}
	if blocked > 0 {
		return errCrewMoveIncomplete
	}
	return nil
}

func (m *crewMover) checkDocsSpace(todo []crewMoveCandidate) error {
	var total int64
	for _, c := range todo {
		total += c.plan.Bytes
	}
	if total == 0 {
		return nil
	}
	free, err := m.opts.free(m.docsAbs)
	if err != nil {
		return err
	}
	need := uint64(total) + uint64(total)/20 + 64<<20
	if free < need {
		return fmt.Errorf("%d bytes free, about %d needed (every Crew is copied and its old folder is kept until --finalize)", free, need)
	}
	return nil
}

// moveOne drives one Crew through its journal states. cand is nil when a half-done move is resumed.
func (m *crewMover) moveOne(ctx context.Context, j *crewMoveJournal, cand *crewMoveCandidate) (err error) {
	opts := m.opts
	defer func() {
		if err != nil {
			j.LastError = err.Error()
			_ = j.save(opts.StateRoot, opts.now())
		}
	}()
	if err := writeCrewMoveActive(opts.StateRoot, j.Folder); err != nil {
		return fmt.Errorf("cannot lock the Crew against the server: %w", err)
	}
	defer clearCrewMoveActive(opts.StateRoot, j.Folder)

	var copied []crewTreeEntry
	// planned: copy into the staging folder.
	if j.State == crewMovePlanned {
		c, err := m.copyToStaging(j)
		if err != nil {
			return err
		}
		copied = c
		j.State = crewMoveStaged
		j.LastError = ""
		if err := j.save(opts.StateRoot, opts.now()); err != nil {
			return err
		}
		if err := opts.hook("staged"); err != nil {
			return err
		}
	}
	// staged: verify the copy against the source, byte for byte.
	if j.State == crewMoveStaged {
		if err := m.verifyStaging(j, copied); err != nil {
			return fmt.Errorf("verification of the copy failed (the source is untouched): %w", err)
		}
		j.State = crewMoveVerified
		if err := j.save(opts.StateRoot, opts.now()); err != nil {
			return err
		}
		if err := opts.hook("verified"); err != nil {
			return err
		}
	}
	// verified: switch.
	if j.State == crewMoveVerified {
		j.State = crewMoveSwitching
		if err := j.save(opts.StateRoot, opts.now()); err != nil {
			return err
		}
	}
	if j.State == crewMoveSwitching {
		if err := m.switchFolders(j); err != nil {
			return err
		}
		j.State = crewMoveSwitched
		if err := j.save(opts.StateRoot, opts.now()); err != nil {
			return err
		}
		if err := opts.hook("switched"); err != nil {
			return err
		}
	}
	// switched: the registry records the Crew as shared, with its old path as an alias.
	if j.State == crewMoveSwitched {
		if err := m.registry.Register(projectOwnerRecord{Product: "work", Folder: j.Folder, OwnerID: j.Owner, ProjectID: j.ProjectID, Shared: true, Aliases: []string{j.Source}}); err != nil {
			return fmt.Errorf("cannot record the move in the owner registry (the Crew is at %s but not yet registered; re-run to finish): %w", j.Dest, err)
		}
		forgetCrewLocationCaches()
		j.State = crewMoveRegistered
		if err := j.save(opts.StateRoot, opts.now()); err != nil {
			return err
		}
		if err := opts.hook("registered"); err != nil {
			return err
		}
	}
	// registered: CLI runtime links follow the Crew, then the whole Crew is verified in its new place.
	if j.State == crewMoveRegistered {
		n, err := cliruntime.RepointProjectLinks(opts.StateRoot, j.OldAbs, j.NewAbs)
		if err != nil {
			return fmt.Errorf("cannot repoint the CLI runtimes of the Crew: %w", err)
		}
		j.LinksRepointed += n
		if err := m.verifyMoved(j); err != nil {
			return fmt.Errorf("verification of the moved Crew failed: %w", err)
		}
		j.State = crewMoveDone
		j.LastError = ""
		if err := j.save(opts.StateRoot, opts.now()); err != nil {
			return err
		}
	}
	return nil
}

func (m *crewMover) copyToStaging(j *crewMoveJournal) ([]crewTreeEntry, error) {
	if err := ensureSharedCrewRoot(m.docsAbs); err != nil {
		return nil, fmt.Errorf("cannot prepare %s: %w", workspaceref.SharedCrewRoot, err)
	}
	if err := m.docs.MkdirAll(filepath.ToSlash(filepath.Dir(j.Staging)), 0o700); err != nil {
		return nil, err
	}
	// A staging folder left by an earlier attempt is only ever this Crew's own partial copy.
	if err := m.docs.RemoveAll(j.Staging); err != nil {
		return nil, fmt.Errorf("cannot clear an earlier partial copy: %w", err)
	}
	src, err := openProjectDir(m.docs, j.Owner, workspaceref.CrewProjectsRoot, j.Folder)
	if err != nil {
		return nil, err
	}
	defer src.Close()
	before, _, err := walkCrewTree(src)
	if err != nil {
		return nil, err
	}
	if err := m.docs.Mkdir(j.Staging, 0o700); err != nil {
		return nil, err
	}
	dst, err := m.docs.OpenRoot(j.Staging)
	if err != nil {
		return nil, err
	}
	defer dst.Close()
	copied, err := copyCrewTree(src, dst, before, copyOptions{Hook: func(_ string, n int) error { return m.opts.hook(fmt.Sprintf("copy:%d", n)) }})
	if err != nil {
		return nil, err
	}
	if err := m.opts.hook("copied"); err != nil {
		return nil, err
	}
	// The source must not have changed under the copy.
	after, _, err := walkCrewTree(src)
	if err != nil {
		return nil, err
	}
	if same, why := sameCrewSnapshot(before, after); !same {
		return nil, fmt.Errorf("the Crew changed while it was being copied (%s); nothing was moved, run again when it is quiet", why)
	}
	j.Tree = crewTreeSummaryOf(copied)
	j.Tree.Digest = crewTreeDigest(copied)
	return copied, nil
}

// verifyStaging compares the staging copy with the source: every entry, size, link target and content hash.
func (m *crewMover) verifyStaging(j *crewMoveJournal, copied []crewTreeEntry) error {
	src, err := openProjectDir(m.docs, j.Owner, workspaceref.CrewProjectsRoot, j.Folder)
	if err != nil {
		return err
	}
	defer src.Close()
	if copied == nil {
		// Resumed after the copy: hash the source again (the copy's own hashes were in memory).
		if copied, err = hashCrewTree(src); err != nil {
			return err
		}
	}
	dst, err := m.docs.OpenRoot(j.Staging)
	if err != nil {
		return err
	}
	defer dst.Close()
	got, err := hashCrewTree(dst)
	if err != nil {
		return err
	}
	if err := compareCrewTrees(copied, got); err != nil {
		return err
	}
	if digest := crewTreeDigest(got); j.Tree.Digest != "" && digest != j.Tree.Digest {
		return fmt.Errorf("the copy's digest %s differs from the journal's %s", digest, j.Tree.Digest)
	} else if j.Tree.Digest == "" {
		j.Tree = crewTreeSummaryOf(got)
		j.Tree.Digest = digest
	}
	// Still unchanged right before the switch.
	now, _, err := walkCrewTree(src)
	if err != nil {
		return err
	}
	if same, why := sameCrewSnapshot(stripHashes(copied), stripHashes(now)); !same {
		return fmt.Errorf("the Crew changed during verification (%s)", why)
	}
	return nil
}

func stripHashes(entries []crewTreeEntry) []crewTreeEntry {
	out := make([]crewTreeEntry, len(entries))
	copy(out, entries)
	for i := range out {
		out[i].Hash = ""
	}
	return out
}

// switchFolders does the two renames, tolerating whichever of them an earlier attempt already did.
func (m *crewMover) switchFolders(j *crewMoveJournal) error {
	exists := func(rel string) bool { _, err := m.docs.Lstat(rel); return err == nil }
	if err := m.opts.hook("before-switch"); err != nil {
		return err
	}
	switch {
	case exists(j.Dest) && exists(j.Staging) && exists(j.Source):
		return fmt.Errorf("%s, %s and %s all exist: resolve by hand", j.Source, j.Staging, j.Dest)
	case !exists(j.Dest) && exists(j.Staging):
		if err := m.docs.Rename(j.Staging, j.Dest); err != nil {
			return fmt.Errorf("switch in %s: %w", j.Dest, err)
		}
	case !exists(j.Dest):
		return fmt.Errorf("neither %s nor %s exists", j.Staging, j.Dest)
	}
	if err := m.opts.hook("after-staging-rename"); err != nil {
		return err
	}
	if exists(j.Source) {
		if err := m.docs.MkdirAll(filepath.ToSlash(filepath.Dir(j.Tombstone)), 0o700); err != nil {
			return err
		}
		if exists(j.Tombstone) {
			// A tombstone from an earlier, rolled-back move: keep it under its own name.
			aside := fmt.Sprintf("%s.%s", j.Tombstone, m.opts.now().Format("20060102T150405"))
			if err := m.docs.Rename(j.Tombstone, aside); err != nil {
				return err
			}
		}
		if err := m.docs.Rename(j.Source, j.Tombstone); err != nil {
			return fmt.Errorf("retire %s: %w", j.Source, err)
		}
	}
	return m.opts.hook("after-source-rename")
}

// verifyMoved is the final verification of a moved Crew: its content against the journal, its manifest, the owner
// registry, the alias, and the access rules, all as the server will apply them.
func (m *crewMover) verifyMoved(j *crewMoveJournal) error {
	dest, err := m.docs.OpenRoot(j.Dest)
	if err != nil {
		return fmt.Errorf("open %s: %w", j.Dest, err)
	}
	defer dest.Close()
	entries, err := hashCrewTree(dest)
	if err != nil {
		return err
	}
	summary := crewTreeSummaryOf(entries)
	if summary.Files != j.Tree.Files || summary.Dirs != j.Tree.Dirs || summary.Symlinks != j.Tree.Symlinks || summary.Bytes != j.Tree.Bytes {
		return fmt.Errorf("counts differ: %+v, journal %+v", summary, j.Tree)
	}
	if digest := crewTreeDigest(entries); digest != j.Tree.Digest {
		return fmt.Errorf("content digest %s differs from the journal's %s", digest, j.Tree.Digest)
	}
	// Permissions of the Crew folder itself (mode and group as the owner's tree had them), and of the shared root.
	if info, err := m.docs.Lstat(j.Dest); err == nil {
		want := fs.FileMode(j.RootMode)
		special := fs.ModeSetgid | fs.ModeSticky
		if info.Mode().Perm()&^0o070 != want.Perm()&^0o070 || info.Mode()&special != want&special {
			return fmt.Errorf("the Crew folder mode is %v, was %v", info.Mode(), want)
		}
		if st, ok := info.Sys().(*syscall.Stat_t); ok && j.RootGID >= 0 && int(st.Gid) != j.RootGID {
			return fmt.Errorf("the Crew folder group is %d, was %d", st.Gid, j.RootGID)
		}
	}
	if info, err := m.docs.Lstat(workspaceref.SharedCrewRoot); err != nil || info.Mode().Perm() != sharedCrewRootMode {
		return fmt.Errorf("%s must have mode %v for slot accounts to reach their Crew", workspaceref.SharedCrewRoot, sharedCrewRootMode)
	}
	// The manifest still says what the journal recorded.
	raw, _, err := readProjectManifest(dest)
	if err != nil {
		return fmt.Errorf("product.json: %w", err)
	}
	var manifest struct {
		Product string `json:"product"`
		ID      string `json:"id"`
	}
	if json.Unmarshal([]byte(raw), &manifest) != nil || !strings.EqualFold(manifest.Product, "work") || (j.ProjectID != "" && manifest.ID != j.ProjectID) {
		return fmt.Errorf("product.json no longer names the Crew %q", j.ProjectID)
	}
	// The owner registry, the alias, and who may reach it.
	rec, ok := m.registry.Lookup("work", j.Folder)
	if !ok || rec.OwnerID != j.Owner || !rec.Shared || !registryListHas(rec.Aliases, j.Source) {
		return fmt.Errorf("the owner registry does not record %s as %q's shared Crew with the alias %s: %+v", j.Folder, j.Owner, j.Source, rec)
	}
	forgetCrewLocationCaches()
	if got := crewAliasesFromRegistry(m.registry)[j.Source]; got != j.Dest {
		return fmt.Errorf("the old path %s resolves to %q, not %s", j.Source, got, j.Dest)
	}
	if owner, ok := crewProjectOwnerID(j.Dest); !ok || owner != j.Owner {
		return fmt.Errorf("the owner of %s resolves to %q", j.Dest, owner)
	}
	if owner, ok := crewProjectOwnerID(j.Source); !ok || owner != j.Owner {
		return fmt.Errorf("the owner of the old path %s resolves to %q", j.Source, owner)
	}
	if workspaceProxyPathIsOtherUser(j.Dest+"/product.json", j.Owner) {
		return fmt.Errorf("the owner is refused raw access to %s", j.Dest)
	}
	for _, spelling := range []string{j.Dest, j.Source} {
		if !workspaceProxyPathIsOtherUser(spelling+"/product.json", "someone-else") {
			return fmt.Errorf("another user is not refused raw access to %s", spelling)
		}
	}
	return nil
}
