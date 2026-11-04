package crewroot

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// MigrateExecutableSources is an explicitly invoked one-off conversion. It is
// not called at startup. Stop the selected instance's writers before using it.
// Skills, knowledge, histories and execution identities are left untouched.
func MigrateExecutableSources(docs string) (int, string, error) {
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
		moves = append(moves, move{Source: source, Target: target})
		return nil
	}
	for _, l := range layouts {
		for _, target := range []string{l.Definitions, l.Code} {
			rel, e := filepath.Rel(l.Crew.Files, target)
			if e != nil {
				return 0, "", e
			}
			if _, e = ResolveContained(l.Crew.Files, rel); e != nil {
				return 0, "", e
			}
		}

		if l.Binding.Kind == "script" {
			err = add(filepath.Join(l.Crew.Resources, "code", l.Binding.ID), l.Code)
		} else {

			// Plan edit history and script execution statistics stay in managed storage.
			code := filepath.Join(l.Runtime, "code")
			err = filepath.WalkDir(code, func(p string, d fs.DirEntry, e error) error {
				if os.IsNotExist(e) {
					return nil
				}
				if e != nil {
					return e
				}
				if !d.IsDir() && d.Name() == "script_metadata.json" {
					return add(p, filepath.Join(l.Runtime, "runs/script-metadata", filepath.Base(filepath.Dir(p))+".json"))
				}
				return nil
			})
			if err != nil {
				return 0, "", err
			}
			entries, e := os.ReadDir(filepath.Join(l.Runtime, "planning"))
			if e != nil && !os.IsNotExist(e) {
				return 0, "", e
			}
			for _, entry := range entries {
				if entry.Name() == "changelog" || entry.Name() == "revisions" {
					continue
				}
				if err = add(filepath.Join(l.Runtime, "planning", entry.Name()), filepath.Join(l.Definitions, "planning", entry.Name())); err != nil {
					return 0, "", err
				}
			}
			for _, name := range []string{"workflow.json", "variables", "code"} {
				target := filepath.Join(l.Definitions, name)
				if name == "code" {
					target = l.Code
				}
				// Resolve against the parent project before creating target directories.
				rel, e := filepath.Rel(l.Crew.Files, target)
				if e != nil {
					return 0, "", e
				}
				if _, e = ResolveContained(l.Crew.Files, rel); e != nil {
					return 0, "", e
				}
				if err = add(filepath.Join(l.Runtime, name), target); err != nil {
					break
				}
			}
		}
		if err != nil {
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
		if err = os.RemoveAll(m.Source); err != nil {
			return i, backup, err
		}
	}
	return len(moves), backup, nil
}
