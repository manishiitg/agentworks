package server

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
)

// crewBackupSummary is backup.json: what a backup run holds, so a rollback or an audit can find and check it.
type crewBackupSummary struct {
	Run     string           `json:"run"`
	Created string           `json:"created"`
	Docs    string           `json:"docs_root"`
	Crews   []crewBackupCrew `json:"crews"`
	Files   []string         `json:"extra_files,omitempty"`
}

type crewBackupCrew struct {
	Folder string          `json:"folder"`
	Owner  string          `json:"owner"`
	Source string          `json:"source"`
	Path   string          `json:"path"`
	Tree   crewTreeSummary `json:"tree"`
}

// makeBackup copies exactly the Crew folders about to move (and the owner registry) under backup-dir/<run>/, then reads
// every backed-up file back and compares it with what the source held, all before the first move. Any failure aborts
// the whole run with nothing changed (the backup directory may hold a partial run; it is never reused).
func (m *crewMover) makeBackup(todo []crewMoveCandidate) error {
	opts := m.opts
	if err := os.MkdirAll(opts.BackupDir, 0o700); err != nil {
		return err
	}
	var total int64
	for _, c := range todo {
		total += c.plan.Bytes
	}
	free, err := opts.free(opts.BackupDir)
	if err != nil {
		return fmt.Errorf("cannot measure the free space of the backup directory: %w", err)
	}
	if need := uint64(total) + uint64(total)/50 + 16<<20; free < need {
		return fmt.Errorf("the backup directory has %d bytes free and about %d are needed", free, need)
	}
	// One directory per run, never reused (two runs in one second get a counter).
	base := "crew-move-" + opts.now().Format("20060102T150405Z")
	run, runDir := base, filepath.Join(opts.BackupDir, base)
	for n := 2; ; n++ {
		err := os.Mkdir(runDir, 0o700)
		if err == nil {
			break
		}
		if !os.IsExist(err) || n > 100 {
			return fmt.Errorf("create backup run %s: %w", runDir, err)
		}
		run = fmt.Sprintf("%s-%d", base, n)
		runDir = filepath.Join(opts.BackupDir, run)
	}
	m.backupRun = run
	m.report.BackupRun = runDir
	summary := crewBackupSummary{Run: run, Created: opts.now().Format("2006-01-02T15:04:05Z"), Docs: m.docsAbs}
	runRoot, err := os.OpenRoot(runDir)
	if err != nil {
		return err
	}
	defer runRoot.Close()
	for _, c := range todo {
		rel := filepath.ToSlash(filepath.Join("crews", c.plan.Owner, c.plan.Folder))
		if err := runRoot.MkdirAll(rel, 0o700); err != nil {
			return err
		}
		// Lock the Crew while it is backed up: the server then runs no turn and the proxy refuses writes for it, so the copy
		// is of a still Crew. A 7 GiB Crew took ~20 minutes to back up on RTS and its chat, report runtime and schedule kept
		// writing, which aborted three attempts (PLAT-442, 2026-10-05).
		if err := writeCrewMoveActive(m.opts.StateRoot, c.plan.Folder); err != nil {
			return err
		}
		unlockBackup := func() { clearCrewMoveActive(m.opts.StateRoot, c.plan.Folder) }
		src, err := openProjectDir(m.docs, c.plan.Owner, workspaceref.CrewProjectsRoot, c.plan.Folder)
		if err != nil {
			unlockBackup()
			return err
		}
		entries, _, err := walkCrewTree(src)
		if err != nil {
			_ = src.Close()
			unlockBackup()
			return fmt.Errorf("%s: %w", c.plan.Folder, err)
		}
		dst, err := runRoot.OpenRoot(rel)
		if err != nil {
			_ = src.Close()
			unlockBackup()
			return err
		}
		// The backup mirrors the Crew's modes and groups as far as the account may (it is a restore source), but never
		// fails the run for a group it cannot set: the content is what a restore needs.
		copied, copyErr := copyCrewTree(src, dst, entries, copyOptions{SkipVanished: true})
		_ = src.Close()
		unlockBackup() // the source is no longer read; the read-back below touches only the backup
		if copyErr != nil && !strings.Contains(copyErr.Error(), "cannot keep group") {
			_ = dst.Close()
			return fmt.Errorf("%s: %w", c.plan.Folder, copyErr)
		}
		if err := m.opts.hook("backup-copied:" + c.plan.Folder); err != nil {
			_ = dst.Close()
			return err
		}
		// Verified readable: every file read back and compared with the source's bytes.
		got, hashErr := hashCrewTree(dst)
		_ = dst.Close()
		if hashErr != nil {
			return fmt.Errorf("%s: the backup cannot be read back: %w", c.plan.Folder, hashErr)
		}
		if copyErr != nil {
			copied = hashedLike(entries, got)
		}
		if err := compareCrewTrees(stripGroup(copied), stripGroup(got)); err != nil {
			return fmt.Errorf("%s: the backup differs from the source: %w", c.plan.Folder, err)
		}
		tree := crewTreeSummaryOf(got)
		tree.Digest = crewTreeDigest(got)
		summary.Crews = append(summary.Crews, crewBackupCrew{Folder: c.plan.Folder, Owner: c.plan.Owner, Source: c.plan.Source, Path: rel, Tree: tree})
		if err := m.opts.hook("backup:" + c.plan.Folder); err != nil {
			return err
		}
	}
	// The owner registry, as it was.
	if raw, err := os.ReadFile(filepath.Join(m.registry.dir, projectOwnerRegistryFile)); err == nil {
		if err := runRoot.MkdirAll("state/ownership", 0o700); err != nil {
			return err
		}
		if err := runRoot.WriteFile("state/ownership/"+projectOwnerRegistryFile, raw, 0o600); err != nil {
			return err
		}
		summary.Files = append(summary.Files, "state/ownership/"+projectOwnerRegistryFile)
	}
	encoded, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return err
	}
	if err := runRoot.WriteFile("backup.json", append(encoded, '\n'), 0o600); err != nil {
		return err
	}
	return m.opts.hook("backup-done")
}

// stripGroup clears group ids so a backup written by an account that cannot set them still compares.
func stripGroup(entries []crewTreeEntry) []crewTreeEntry {
	out := make([]crewTreeEntry, len(entries))
	copy(out, entries)
	for i := range out {
		out[i].GID = -1
	}
	return out
}

// hashedLike carries the source's hashes (from a walk of the backup) onto the source entries.
func hashedLike(source, hashed []crewTreeEntry) []crewTreeEntry {
	byRel := map[string]string{}
	for _, entry := range hashed {
		byRel[entry.Rel] = entry.Hash
	}
	out := make([]crewTreeEntry, len(source))
	copy(out, source)
	for i := range out {
		out[i].Hash = byRel[out[i].Rel]
	}
	return out
}
