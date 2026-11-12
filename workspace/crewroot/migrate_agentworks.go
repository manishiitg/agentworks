package crewroot

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

// One-off PLAT-746-924 move, run before workspace traffic on servers and
// laptops: a Crew's records, runs, database, plans, knowledge and automation
// runtime leave its private root for its .agentworks/ folder. Chats, logins
// and program runtime stay private. Moves are renames (nothing is rewritten);
// the journal lists every one so a stopped instance can be put back. Delete
// this file once every server and laptop has moved.
const agentworksMigrationMarker = "crew-agentworks-v1.json"

type agentworksMove struct {
	Source string `json:"source"`
	Target string `json:"target"`
	// Drop: the target already holds this file, so the source is removed (restore copies it back).
	Drop bool `json:"drop,omitempty"`
}

// regeneratedFile names a marker or lock the platform writes again on use.
func regeneratedFile(name string) bool {
	return name == ".workspace-skills-v1" || strings.HasSuffix(name, ".kb-lock")
}

// describeCopy names one copy with its size and last change, so a person can choose.
func describeCopy(path string, info os.FileInfo) string {
	kind := fmt.Sprintf("%d bytes", info.Size())
	if info.IsDir() {
		kind = "a folder"
	} else if info.Mode()&os.ModeSymlink != 0 {
		kind = "a link"
	}
	return fmt.Sprintf("%s (%s, changed %s)", path, kind, info.ModTime().UTC().Format("2006-01-02 15:04 UTC"))
}

// sameFile reports two regular files with identical bytes.
func sameFile(a, b string, ai, bi os.FileInfo) bool {
	if !ai.Mode().IsRegular() || !bi.Mode().IsRegular() || ai.Size() != bi.Size() {
		return false
	}
	x, err := os.ReadFile(a)
	if err != nil {
		return false
	}
	y, err := os.ReadFile(b)
	return err == nil && bytes.Equal(x, y)
}

type agentworksJournal struct {
	Marker string           `json:"marker"`
	Moves  []agentworksMove `json:"moves"`
}

// planAgentworksMoves lists the leaf moves for one Crew. A destination folder
// that already exists is merged child by child. A file already in .agentworks
// keeps that copy when both are identical, or when it is a regenerated marker
// or lock; a differing file stops the whole move before anything changes.
func planAgentworksMoves(r Roots, kinds map[string]string) ([]agentworksMove, error) {
	var moves []agentworksMove
	planned := map[string]string{} // target -> source of an earlier planned move
	// destination is what target will be once the earlier planned moves are done.
	destination := func(target string) (os.FileInfo, error) {
		for dir := target; ; dir = filepath.Dir(dir) {
			if source, ok := planned[dir]; ok {
				rel, _ := filepath.Rel(dir, target)
				return os.Lstat(filepath.Join(source, rel))
			}
			if dir == filepath.Dir(dir) {
				return os.Lstat(target)
			}
		}
	}
	// onDisk: the target file exists now, not only after an earlier planned move.
	onDisk := func(target string, existing os.FileInfo) bool {
		now, err := os.Lstat(target)
		return err == nil && os.SameFile(now, existing)
	}
	var add func(source, target string) error
	add = func(source, target string) error {
		info, err := os.Lstat(source)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		existing, err := destination(target)
		if os.IsNotExist(err) {
			moves = append(moves, agentworksMove{Source: source, Target: target})
			planned[target] = source
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() && existing.Mode().IsRegular() && onDisk(target, existing) && (regeneratedFile(filepath.Base(target)) || sameFile(source, target, info, existing)) {
			moves = append(moves, agentworksMove{Source: source, Target: target, Drop: true})
			return nil
		}
		if !info.IsDir() || !existing.IsDir() || info.Mode()&os.ModeSymlink != 0 || existing.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("two different copies of the same file: %s and %s. Keep the one you want, move or delete the other, then start again", describeCopy(source, info), describeCopy(target, existing))
		}
		entries, err := os.ReadDir(source)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if err := add(filepath.Join(source, e.Name()), filepath.Join(target, e.Name())); err != nil {
				return err
			}
		}
		return nil
	}
	entries, err := os.ReadDir(r.Private)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	for _, e := range entries {
		name := e.Name()
		source := filepath.Join(r.Private, name)
		switch {
		case settingsPath(name) || strings.HasPrefix(name, "workflow.json."):
			err = add(source, filepath.Join(r.SettingsDir(), name))
		case automationDataPath(name):
			err = add(source, filepath.Join(r.Automation, name))
		case e.IsDir() && (name == "workflows" || name == "scripts" || name == "automations"):
			var children []os.DirEntry
			if children, err = os.ReadDir(source); err != nil {
				return nil, err
			}
			for _, child := range children {
				if !child.IsDir() {
					continue
				}
				runtimeRoot := name + "/" + child.Name()
				kind := kinds[runtimeRoot]
				if kind == "" {
					kind = map[string]string{"workflows": "workflow"}[name]
				}
				var items []os.DirEntry
				if items, err = os.ReadDir(filepath.Join(source, child.Name())); err != nil {
					return nil, err
				}
				for _, item := range items {
					from := filepath.Join(source, child.Name(), item.Name())
					to := filepath.Join(r.Automation, name, child.Name(), item.Name())
					if executableSettingsPath(kind, item.Name()) {
						to = filepath.Join(r.SettingsDir(), name, child.Name(), item.Name())
					}
					if err = add(from, to); err != nil {
						return nil, err
					}
				}
			}
		}
		if err != nil {
			return nil, err
		}
	}
	// A server copy of a laptop Crew kept definitions and code under _files/.agentworks; they join its resource root.
	if r.SeparateFiles() && !r.Local {
		for _, name := range []string{"workflows", "code"} {
			if err := add(filepath.Join(r.Files, AutomationFolder, name), filepath.Join(r.Automation, name)); err != nil {
				return nil, err
			}
		}
	}
	// A server Crew's shared database folder (db/assets, db/reports) joins the database in .agentworks/db.
	if r.Resources != r.Automation {
		if err := add(filepath.Join(r.Resources, "db"), filepath.Join(r.Automation, "db")); err != nil {
			return nil, err
		}
	}
	return moves, nil
}

func moveEntry(source, target string) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o770); err != nil {
		return err
	}
	err := os.Rename(source, target)
	if errors.Is(err, syscall.EXDEV) {
		if err = copyPrivateEntry(source, target); err == nil {
			err = os.RemoveAll(source)
		}
	}
	return err
}

// RestoreAgentworksMove puts a stopped instance back from a move journal.
func RestoreAgentworksMove(journalPath string) error {
	raw, err := os.ReadFile(journalPath)
	if err != nil {
		return err
	}
	var journal agentworksJournal
	if err = json.Unmarshal(raw, &journal); err != nil {
		return err
	}
	for i := len(journal.Moves) - 1; i >= 0; i-- {
		m := journal.Moves[i]
		if _, err := os.Lstat(m.Target); os.IsNotExist(err) {
			continue
		}
		if m.Drop {
			if err := copyPrivateEntry(m.Target, m.Source); err != nil {
				return err
			}
			continue
		}
		if err := moveEntry(m.Target, m.Source); err != nil {
			return err
		}
	}
	return os.Remove(journal.Marker)
}

// MigrateToAgentworks moves every registered Crew once. It writes the journal
// before the first rename and the marker after the last one.
func MigrateToAgentworks(docs string) (count int, journalPath string, err error) {
	state, err := StateRoot()
	if err != nil {
		return 0, "", err
	}
	marker := filepath.Join(state, "migrations", agentworksMigrationMarker)
	if _, e := os.Stat(marker); e == nil {
		return 0, "", nil
	} else if !os.IsNotExist(e) {
		return 0, "", e
	}
	all, err := records()
	if err != nil {
		return 0, "", err
	}
	kinds := map[string]map[string]string{}
	if layouts, e := ExecutableLayouts(docs); e == nil {
		for _, l := range layouts {
			if kinds[l.Crew.ID] == nil {
				kinds[l.Crew.ID] = map[string]string{}
			}
			kinds[l.Crew.ID][l.Binding.RuntimeRoot] = l.Binding.Kind
		}
	}
	var keys []string
	for key := range all {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	journal := agentworksJournal{Marker: marker}
	var crews []Roots
	for _, key := range keys {
		rec := all[key]
		if rec.Product != "work" {
			continue
		}
		r, e := rootsForOperation(docs, rec, true)
		if e != nil {
			fmt.Printf("[CREW_AGENTWORKS_MIGRATION] skipped folder=%s: %v\n", rec.Folder, e)
			continue
		}
		if _, e := os.Stat(r.Files); e != nil {
			fmt.Printf("[CREW_AGENTWORKS_MIGRATION] missing folder=%s (unchanged)\n", rec.Folder)
			continue
		}
		moves, e := planAgentworksMoves(r, kinds[r.ID])
		if e != nil {
			return 0, "", fmt.Errorf("Crew %s: %w", rec.Folder, e)
		}
		journal.Moves = append(journal.Moves, moves...)
		crews = append(crews, r)
	}
	backup := filepath.Join(state, "migration-backups", "crew-agentworks-"+time.Now().UTC().Format("20060102T150405"))
	if err = os.MkdirAll(backup, 0o700); err != nil {
		return 0, "", err
	}
	journalPath = filepath.Join(backup, "journal.json")
	raw, err := json.MarshalIndent(journal, "", "  ")
	if err != nil {
		return 0, journalPath, err
	}
	if err = os.WriteFile(journalPath, raw, 0o600); err != nil {
		return 0, journalPath, err
	}
	for _, r := range crews {
		if err = r.EnsureAutomation(); err != nil {
			return count, journalPath, fmt.Errorf("prepare %s: %w", r.Automation, err)
		}
	}
	dropped := 0
	for _, m := range journal.Moves {
		if m.Drop {
			if err = os.Remove(m.Source); err != nil {
				return count, journalPath, fmt.Errorf("remove duplicate %s: %w; put back with --restore-crew-agentworks %s", m.Source, err, journalPath)
			}
			fmt.Printf("[CREW_AGENTWORKS_MIGRATION] kept .agentworks copy, removed duplicate %s\n", m.Source)
			dropped++
			continue
		}
		if err = moveEntry(m.Source, m.Target); err != nil {
			return count, journalPath, fmt.Errorf("move %s: %w; put back with --restore-crew-agentworks %s", m.Source, err, journalPath)
		}
		count++
	}
	// On a server the moved folders get the Crew folder's group (the owner's
	// slot) and group write, so a workflow run as the owner's slot can write
	// its runs and database. settings/ stays group read-only.
	for _, r := range crews {
		if r.Local {
			continue
		}
		info, e := os.Stat(r.Files)
		if e != nil {
			continue
		}
		gid := fileGroup(info)
		for _, m := range journal.Moves {
			if !Within(r.Automation, m.Target) || Within(r.SettingsDir(), m.Target) {
				continue
			}
			if entry, e := os.Lstat(m.Target); e == nil && entry.IsDir() {
				_ = os.Lchown(m.Target, -1, gid)
				_ = os.Chmod(m.Target, entry.Mode().Perm()|0o070|os.ModeSetgid)
			}
		}
	}
	if err = os.MkdirAll(filepath.Dir(marker), 0o700); err != nil {
		return count, journalPath, err
	}
	if err = os.WriteFile(marker, []byte(journalPath+"\n"), 0o600); err != nil {
		return count, journalPath, err
	}
	fmt.Printf("[CREW_AGENTWORKS_MIGRATION] crews=%d moved=%d duplicates_removed=%d journal=%s\n", len(crews), count, dropped, journalPath)
	return count, journalPath, nil
}
