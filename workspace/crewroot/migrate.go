package crewroot

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/filemetadata"
)

// One-off PLAT-856-383 conversion. Called before workspace traffic on servers
// and desktops; no runtime compatibility paths remain after conversion.
type PrivateMigration struct {
	Marker  string        `json:"marker"`
	Entries []privateMove `json:"entries"`
}
type privateMove struct {
	Source, Target, Saved string
	UnbackedCache         bool `json:"unbacked_cache,omitempty"`
}

func isPrivateCache(path string) bool {
	name := filepath.Base(path)
	return name == ".sandbox-cache" || name == ".report-cache"
}

func copyPrivateEntry(source, target string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		link, err := os.Readlink(source)
		if err != nil {
			return err
		}
		return os.Symlink(link, target)
	}
	if info.IsDir() {
		if err := os.MkdirAll(target, 0700); err != nil {
			return err
		}
		entries, err := os.ReadDir(source)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if err := copyPrivateEntry(filepath.Join(source, e.Name()), filepath.Join(target, e.Name())); err != nil {
				return err
			}
		}
	} else {
		in, err := os.Open(source)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(out, in)
		if copyErr == nil {
			copyErr = out.Sync()
		}
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Open(target)
	if err != nil {
		return err
	}
	defer out.Close()
	if err := filemetadata.Preserve(out, in); err != nil {
		return err
	}
	if info.IsDir() {
		if err := out.Chmod(info.Mode().Perm() | (info.Mode() & os.ModeSetgid)); err != nil {
			return err
		}
	}
	return os.Chtimes(target, info.ModTime(), info.ModTime())
}

// EnsurePrivateRoot preserves the existing Crew slot group. Only the exact
// private Crew tree is granted by the sandbox, never its shared parent.
func EnsurePrivateRoot(docs, id, workingRoot string) (string, error) {
	private, err := PrivateRoot(docs, id)
	if err != nil {
		return "", err
	}
	for _, parent := range []string{filepath.Join(docs, "_system"), filepath.Join(docs, filepath.FromSlash(PrivateDirectory))} {
		if err := os.MkdirAll(parent, 0711); err != nil {
			return "", err
		}
		info, err := os.Stat(parent)
		if err != nil {
			return "", err
		}
		if err := os.Chmod(parent, info.Mode()|0011); err != nil {
			return "", err
		}
	}
	if err := os.MkdirAll(private, 0750); err != nil {
		return "", err
	}
	source, err := os.Open(workingRoot)
	if err != nil {
		return "", err
	}
	defer source.Close()
	target, err := os.Open(private)
	if err != nil {
		return "", err
	}
	defer target.Close()
	if err := filemetadata.Preserve(target, source); err != nil {
		return "", err
	}
	if err := target.Chmod(0750 | os.ModeSetgid); err != nil {
		return "", err
	}
	return private, nil
}

func privateEntries(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, entry := range entries {
		name := entry.Name()
		if privatePath(name) {
			paths = append(paths, name)
			continue
		}
		if name != "db" && name != "triggers" {
			continue
		}
		children, err := os.ReadDir(filepath.Join(root, name))
		if err != nil {
			return nil, err
		}
		for _, child := range children {
			rel := name + "/" + child.Name()
			if privatePath(rel) {
				paths = append(paths, rel)
			}
		}
	}
	return paths, nil
}

// RestorePrivateStateBackup is only for the stopped-writer deployment restore
// trap. It restores this conversion's platform entries, never user repositories.
func RestorePrivateStateBackup(backup string) error {
	raw, err := os.ReadFile(filepath.Join(backup, "journal.json"))
	if err != nil {
		return err
	}
	var journal PrivateMigration
	if err = json.Unmarshal(raw, &journal); err != nil {
		return err
	}
	for _, entry := range journal.Entries {
		if entry.UnbackedCache {
			// Cache bytes were never copied or changed. Undo only their rename.
			if _, err := os.Lstat(entry.Target); os.IsNotExist(err) {
				continue
			} else if err != nil {
				return err
			}
			if _, err := os.Lstat(entry.Source); err == nil {
				return fmt.Errorf("cannot restore cache over existing source: %s", entry.Source)
			} else if !os.IsNotExist(err) {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(entry.Source), 0700); err != nil {
				return err
			}
			if err := os.Rename(entry.Target, entry.Source); err != nil {
				return err
			}
			continue
		}
		if err := os.RemoveAll(entry.Target); err != nil {
			return err
		}
		if err := os.RemoveAll(entry.Source); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(entry.Source), 0700); err != nil {
			return err
		}
		if err := copyPrivateEntry(entry.Saved, entry.Source); err != nil {
			return err
		}
	}
	if err := os.Remove(journal.Marker); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func MigratePrivateState(docs string) (count int, backup string, err error) {
	state, err := StateRoot()
	if err != nil {
		return 0, "", err
	}
	if err := os.MkdirAll(state, 0700); err != nil {
		return 0, "", err
	}
	marker := filepath.Join(state, "migrations", "crew-private-state-v1.json")
	if _, e := os.Stat(marker); e == nil {
		return 0, "", nil
	} else if !os.IsNotExist(e) {
		return 0, "", e
	}
	all, err := records()
	if err != nil {
		return 0, "", err
	}
	journal := PrivateMigration{Marker: marker}
	registered := map[string]bool{}
	var ids []string
	for key := range all {
		ids = append(ids, key)
	}
	sort.Strings(ids)
	for _, key := range ids {
		rec := all[key]
		if rec.Product != "work" {
			continue
		}
		if rec.Folder == "" || strings.HasPrefix(rec.Folder, ".") || strings.ContainsAny(rec.Folder, "/\\") {
			return 0, "", fmt.Errorf("invalid Crew folder")
		}
		private, e := PrivateRoot(docs, rec.ID)
		if e != nil {
			return 0, "", e
		}
		root := filepath.Join(docs, "Crew", rec.Folder)
		registered[rec.Folder] = true
		if _, e := os.Stat(root); os.IsNotExist(e) {
			fmt.Printf("[CREW_STATE_MIGRATION] missing registered folder=%s\n", rec.Folder)
			continue
		} else if e != nil {
			return 0, "", e
		}
		sources := []string{root}
		if rec.LocalRoot != "" {
			sources = append(sources, filepath.Join(rec.LocalRoot, ".agentworks"))
		}
		for _, source := range sources {
			if e := unchangedDirectory(source, true); e != nil {
				return 0, "", e
			}
			rels, e := privateEntries(source)
			if e != nil {
				return 0, "", e
			}
			for _, rel := range rels {
				target := filepath.Join(private, filepath.FromSlash(rel))
				if _, e := os.Lstat(target); e == nil {
					return 0, "", fmt.Errorf("private destination already exists: %s", target)
				} else if !os.IsNotExist(e) {
					return 0, "", e
				}
				for _, existing := range journal.Entries {
					if existing.Target == target {
						return 0, "", fmt.Errorf("duplicate platform record: %s", rel)
					}
				}
				journal.Entries = append(journal.Entries, privateMove{Source: filepath.Join(source, filepath.FromSlash(rel)), Target: target})
			}
		}
	}
	entries, e := os.ReadDir(filepath.Join(docs, "Crew"))
	if e != nil && !os.IsNotExist(e) {
		return 0, "", e
	}
	for _, entry := range entries {
		if entry.IsDir() && !registered[entry.Name()] {
			fmt.Printf("[CREW_STATE_MIGRATION] unmatched folder=%s (unchanged)\n", entry.Name())
		}
	}
	backup = filepath.Join(state, "migration-backups", "crew-private-state-"+time.Now().UTC().Format("20060102T150405.000000000"))
	if err = checkMigrationSpace(docs, state, journal.Entries); err != nil {
		return 0, "", err
	}
	if err = os.MkdirAll(backup, 0700); err != nil {
		return 0, "", err
	}
	// Copy every affected record before moving any of them.
	for i := range journal.Entries {
		if journal.Entries[i].UnbackedCache {
			fmt.Printf("[CREW_STATE_MIGRATION] unreadable cache will move unchanged without backup: %s\n", journal.Entries[i].Source)
			continue
		}
		journal.Entries[i].Saved = filepath.Join(backup, fmt.Sprintf("%06d", i))
		if err = copyPrivateEntry(journal.Entries[i].Source, journal.Entries[i].Saved); err != nil {
			return 0, backup, err
		}
	}
	raw, err := json.MarshalIndent(journal, "", "  ")
	if err != nil {
		return 0, backup, err
	}
	if err = os.WriteFile(filepath.Join(backup, "journal.json"), raw, 0600); err != nil {
		return 0, backup, err
	}
	defer func() {
		if err != nil {
			if restoreErr := RestorePrivateStateBackup(backup); restoreErr != nil {
				err = fmt.Errorf("%w; restore backup %s: %v", err, backup, restoreErr)
			}
		}
	}()
	for _, rec := range all {
		if rec.Product != "work" {
			continue
		}
		root := filepath.Join(docs, "Crew", rec.Folder)
		if _, e := os.Stat(root); os.IsNotExist(e) {
			continue
		}
		if _, err = EnsurePrivateRoot(docs, rec.ID, root); err != nil {
			return 0, backup, err
		}
	}
	for _, entry := range journal.Entries {
		if err = os.MkdirAll(filepath.Dir(entry.Target), 0750); err != nil {
			return 0, backup, err
		}
		// Hosted entries stay on one filesystem. Rename preserves even a
		// slot-owned 0700 cache without reading or changing its permissions.
		err = os.Rename(entry.Source, entry.Target)
		if errors.Is(err, syscall.EXDEV) {
			if entry.UnbackedCache {
				return 0, backup, fmt.Errorf("unreadable cache cannot move across filesystems: %s", entry.Source)
			}
			err = copyPrivateEntry(entry.Source, entry.Target)
			if err == nil {
				err = os.RemoveAll(entry.Source)
			}
		}
		if err != nil {
			return 0, backup, err
		}
		count++
	}
	// An old local private directory is only an empty anchor now. Never remove
	// a selected laptop folder or nonempty data during this cleanup.
	for _, rec := range all {
		if rec.Product == "work" && rec.LocalRoot != "" {
			_ = os.Remove(filepath.Join(docs, "Crew", rec.Folder))
		}
	}
	if err = os.MkdirAll(filepath.Dir(marker), 0700); err != nil {
		return count, backup, err
	}
	err = os.WriteFile(marker, []byte(backup+"\n"), 0600)
	if err == nil {
		unbacked := 0
		for _, entry := range journal.Entries {
			if entry.UnbackedCache {
				unbacked++
			}
		}
		fmt.Printf("[CREW_STATE_MIGRATION] backed_up_entries=%d unreadable_caches_moved_unchanged=%d\n", len(journal.Entries)-unbacked, unbacked)
	}
	return count, backup, err
}
