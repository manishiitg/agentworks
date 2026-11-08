package crewroot

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// MigrateCrewScripts is an explicitly invoked one-off conversion. It is
// not called at startup. Stop the selected instance's writers before using it.
// Reusable script source and runtime data only. Workflows use scripts/migrate-goal-to-crew.py so a
// source-only move cannot leave their records in the retired folder.
func MigrateCrewScripts(docs string) (int, string, error) {
	docs, err := filepath.EvalSymlinks(docs)
	if err != nil {
		return 0, "", err
	}
	layouts, err := ExecutableLayouts(docs)
	if err != nil {
		return 0, "", err
	}
	type move struct{ Source, Target, Backup string }
	var moves []move
	add := func(source, target string) error {
		if source == target {
			return nil
		}
		if _, err := os.Lstat(source); os.IsNotExist(err) {
			return nil
		} else if err != nil {
			return err
		}
		if _, err := os.Lstat(target); err == nil {
			return fmt.Errorf("destination already exists: %s", target)
		} else if !os.IsNotExist(err) {
			return err
		}
		if err := unchangedDirectory(source, false); err != nil {
			return err
		}
		moves = append(moves, move{Source: source, Target: target})
		return nil
	}
	for _, l := range layouts {
		if l.Binding.Kind != "script" {
			continue
		}
		for _, target := range []string{l.Definitions, l.Code} {
			rel, e := filepath.Rel(l.Crew.Files, target)
			if e != nil {
				return 0, "", e
			}
			if _, e = ResolveContained(l.Crew.Files, rel); e != nil {
				return 0, "", e
			}
		}

		err = add(filepath.Join(l.Crew.Resources, "code", l.Binding.ID), l.Code)
		if err != nil {
			return 0, "", err
		}
		if err = add(filepath.Join(docs, filepath.FromSlash(l.Binding.Root)), l.Runtime); err != nil {
			return 0, "", err
		}
	}
	if len(moves) == 0 {
		return 0, "", nil
	}
	state, err := StateRoot()
	if err != nil {
		return 0, "", err
	}
	backup := filepath.Join(state, "migrations", "crew-automation-"+time.Now().UTC().Format("20060102T150405.000000000"))
	if err = os.MkdirAll(backup, 0700); err != nil {
		return 0, "", err
	}
	// Complete a plain backup before changing any source.
	for i := range moves {
		moves[i].Backup = filepath.Join(backup, fmt.Sprintf("%03d", i))
		if err = copyPrivateEntry(moves[i].Source, moves[i].Backup); err != nil {
			return 0, backup, err
		}
		if err = verifyScriptMigrationCopy(moves[i].Source, moves[i].Backup); err != nil {
			return 0, backup, err
		}
	}
	raw, _ := json.MarshalIndent(moves, "", "  ")
	if err = os.WriteFile(filepath.Join(backup, "moves.json"), raw, 0600); err != nil {
		return 0, backup, err
	}
	for i, m := range moves {
		if err = os.MkdirAll(filepath.Dir(m.Target), 0700); err != nil {
			return i, backup, err
		}
		if err = copyPrivateEntry(m.Source, m.Target); err != nil {
			return i, backup, err
		}
		if err = verifyScriptMigrationCopy(m.Source, m.Target); err != nil {
			return i, backup, err
		}
		if err = os.RemoveAll(m.Source); err != nil {
			return i, backup, err
		}
	}
	return len(moves), backup, nil
}

// Read every copied regular file before removing its original. Links remain
// links; verifying a backup never follows them into another project's data.
func verifyScriptMigrationCopy(source, target string) error {
	digest := func(path string) ([sha256.Size]byte, error) {
		var result [sha256.Size]byte
		file, err := os.Open(path)
		if err != nil {
			return result, err
		}
		defer file.Close()
		h := sha256.New()
		if _, err = io.Copy(h, file); err != nil {
			return result, err
		}
		copy(result[:], h.Sum(nil))
		return result, nil
	}
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		copied := filepath.Join(target, rel)
		old, err := os.Lstat(path)
		if err != nil {
			return err
		}
		got, err := os.Lstat(copied)
		if err != nil {
			return err
		}
		if old.Mode() != got.Mode() {
			return fmt.Errorf("script migration changed mode: %s", rel)
		}
		if old.Mode()&os.ModeSymlink != 0 {
			a, e := os.Readlink(path)
			if e != nil {
				return e
			}
			b, e := os.Readlink(copied)
			if e != nil {
				return e
			}
			if a != b {
				return fmt.Errorf("script migration changed link: %s", rel)
			}
		} else if old.Mode().IsRegular() {
			a, e := digest(path)
			if e != nil {
				return e
			}
			b, e := digest(copied)
			if e != nil {
				return e
			}
			if old.Size() != got.Size() || a != b {
				return fmt.Errorf("script migration copy differs: %s", rel)
			}
		}
		return nil
	})
}
