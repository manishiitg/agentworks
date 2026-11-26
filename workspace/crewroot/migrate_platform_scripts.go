package crewroot

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// One-off move (#4009), run before workspace traffic on servers and laptops: the platform's own scripts leave code/
// for .agentworks/ (dashboard scripts code/reports/managed/ -> dashboards/, Watcher checks code/triggers/ ->
// watchers/, motion compositions code/motion/ -> motion/), and saved pages and records that name the old paths are
// rewritten. Everything moved is copied to a backup first. Delete this file once every server and laptop has moved.
const platformScriptsMigrationMarker = "platform-scripts-v1.json"

// platformScriptMoves pairs each old folder under code/ with its new folder; replace rewrites saved references.
var platformScriptMoves = []struct{ old, new string }{
	{"code/reports/managed", "dashboards"},
	{"code/triggers", "watchers"},
	{"code/motion", "motion"},
}

var platformScriptRewrites = []struct{ old, new string }{
	{"code/reports/managed/", "dashboards/"},
	{"code/triggers/", "watchers/"},
	{"code/motion/", "motion/"},
}

// platformScriptPlace is one root whose code/ folders move: from (where code/ was), to (the new home), and the
// folders whose text files may name the old paths.
type platformScriptPlace struct {
	label    string
	from, to string
	only     []string // when set, only these old folders move from here
	inCode   bool     // from is itself a code/ folder (a project's workflow), so the old folders sit without "code/"
	rewrite  []string
}

// MigratePlatformScripts moves every project's, workflow's and watcher folder's platform scripts once.
func MigratePlatformScripts(docs string) (moved, rewritten int, backup string, err error) {
	state, err := StateRoot()
	if err != nil {
		return 0, 0, "", err
	}
	marker := filepath.Join(state, "migrations", platformScriptsMigrationMarker)
	if _, e := os.Stat(marker); e == nil {
		return 0, 0, "", nil
	} else if !os.IsNotExist(e) {
		return 0, 0, "", e
	}
	places, err := platformScriptPlaces(docs)
	if err != nil {
		return 0, 0, "", err
	}
	return runPlatformScriptPlaces(state, marker, "platform-scripts-", places)
}

// MigrateExecutablePlatformScripts is the same move for each project's own workflows and scripts (#4009): their
// code/ is .agentworks/code/<id>/ and their other folders .agentworks/workflows/<id>/, which the first pass missed.
// It has its own marker because servers that already ran the first pass must still run this one.
func MigrateExecutablePlatformScripts(docs string) (moved, rewritten int, backup string, err error) {
	state, err := StateRoot()
	if err != nil {
		return 0, 0, "", err
	}
	marker := filepath.Join(state, "migrations", "platform-scripts-executables-v1.json")
	if _, e := os.Stat(marker); e == nil {
		return 0, 0, "", nil
	} else if !os.IsNotExist(e) {
		return 0, 0, "", e
	}
	layouts, err := ExecutableLayouts(docs)
	if err != nil {
		return 0, 0, "", err
	}
	var places []platformScriptPlace
	for _, l := range layouts {
		if l.Code == "" || l.Runtime == "" {
			continue
		}
		db := filepath.Join(l.Runtime, "db")
		if l.Crew.DatabaseRoot != "" {
			db = filepath.Join(l.Crew.Automation, filepath.FromSlash(l.Crew.DatabaseRoot))
		} else if l.Binding.Kind == "script" {
			db = filepath.Join(l.Crew.Automation, "db")
		}
		places = append(places, platformScriptPlace{label: "project " + l.Crew.Virtual + " " + l.Binding.Kind + " " + l.Binding.ID, from: l.Code, to: l.Runtime, inCode: true, rewrite: []string{filepath.Join(db, "reports"), l.Settings}})
	}
	return runPlatformScriptPlaces(state, marker, "platform-scripts-executables-", places)
}

func runPlatformScriptPlaces(state, marker, name string, places []platformScriptPlace) (moved, rewritten int, backup string, err error) {
	backup = filepath.Join(state, "migration-backups", name+time.Now().UTC().Format("20060102T150405"))
	for i, p := range places {
		m, r, e := migratePlatformScriptPlace(p, filepath.Join(backup, fmt.Sprintf("%03d", i)))
		if e != nil {
			return moved, rewritten, backup, fmt.Errorf("%s: %w", p.label, e)
		}
		if m+r > 0 {
			fmt.Printf("[PLATFORM_SCRIPTS_MIGRATION] %s moved=%d rewritten=%d\n", p.label, m, r)
		}
		moved, rewritten = moved+m, rewritten+r
	}
	if err = os.MkdirAll(filepath.Dir(marker), 0o700); err != nil {
		return moved, rewritten, backup, err
	}
	if err = os.WriteFile(marker, []byte(backup+"\n"), 0o600); err != nil {
		return moved, rewritten, backup, err
	}
	fmt.Printf("[PLATFORM_SCRIPTS_MIGRATION] places=%d moved=%d rewritten=%d backup=%s\n", len(places), moved, rewritten, backup)
	return moved, rewritten, backup, nil
}

func platformScriptPlaces(docs string) ([]platformScriptPlace, error) {
	all, err := records()
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(all))
	for key := range all {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var places []platformScriptPlace
	for _, key := range keys {
		rec := all[key]
		if rec.Product != "work" {
			continue
		}
		r, e := rootsForOperation(docs, rec, true)
		if e != nil {
			fmt.Printf("[PLATFORM_SCRIPTS_MIGRATION] skipped project folder=%s: %v\n", rec.Folder, e)
			continue
		}
		if _, e := os.Stat(r.Resources); e != nil {
			continue
		}
		db := filepath.Join(r.Automation, "db")
		if r.DatabaseRoot != "" {
			db = filepath.Join(r.Automation, filepath.FromSlash(r.DatabaseRoot))
		}
		rewrite := []string{filepath.Join(db, "reports"), r.SettingsDir()}
		places = append(places, platformScriptPlace{label: "project " + rec.Folder, from: r.Resources, to: r.Automation, rewrite: rewrite})
		// A laptop project made before .agentworks/ may still hold dashboard scripts in its top-level code/.
		if r.Files != r.Resources {
			places = append(places, platformScriptPlace{label: "project " + rec.Folder + " (top level)", from: r.Files, to: r.Automation, only: []string{"code/reports/managed"}, rewrite: rewrite})
		}
	}
	// Workflows and each person's Watcher folders keep their own code/ and records in their folder.
	for _, pattern := range []string{"Workflow/*", "_users/*/watchers/*/*"} {
		dirs, _ := filepath.Glob(filepath.Join(docs, filepath.FromSlash(pattern)))
		sort.Strings(dirs)
		for _, dir := range dirs {
			if info, e := os.Lstat(dir); e != nil || !info.IsDir() {
				continue
			}
			rel, _ := filepath.Rel(docs, dir)
			places = append(places, platformScriptPlace{label: filepath.ToSlash(rel), from: dir, to: dir, rewrite: []string{dir}})
		}
	}
	return places, nil
}

func migratePlatformScriptPlace(p platformScriptPlace, backup string) (moved, rewritten int, err error) {
	for _, m := range platformScriptMoves {
		if len(p.only) > 0 && !containsString(p.only, m.old) {
			continue
		}
		old := m.old
		if p.inCode {
			old = strings.TrimPrefix(old, "code/")
		}
		source := filepath.Join(p.from, filepath.FromSlash(old))
		info, e := os.Lstat(source)
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return moved, rewritten, e
		}
		if !info.IsDir() {
			continue
		}
		if err = os.MkdirAll(filepath.Join(backup, filepath.FromSlash(path.Dir(m.old))), 0o700); err != nil {
			return moved, rewritten, err
		}
		if err = copyPrivateEntry(source, filepath.Join(backup, filepath.FromSlash(m.old))); err != nil {
			return moved, rewritten, fmt.Errorf("back up %s: %w", source, err)
		}
		n, e := mergeInto(source, filepath.Join(p.to, m.new))
		if e != nil {
			return moved, rewritten, e
		}
		moved += n
		removeEmptyUp(source, p.from)
	}
	if moved == 0 {
		return 0, 0, nil
	}
	for _, dir := range p.rewrite {
		n, e := rewritePlatformScriptPaths(dir, backup)
		if e != nil {
			return moved, rewritten, e
		}
		rewritten += n
	}
	return moved, rewritten, nil
}

// mergeInto moves each entry of source into target; an entry already in target stays and the old copy is kept in
// the backup only.
func mergeInto(source, target string) (int, error) {
	entries, err := os.ReadDir(source)
	if err != nil {
		return 0, err
	}
	moved := 0
	for _, e := range entries {
		from, to := filepath.Join(source, e.Name()), filepath.Join(target, e.Name())
		if existing, err := os.Lstat(to); err == nil {
			if existing.IsDir() && e.IsDir() {
				n, err := mergeInto(from, to)
				moved += n
				if err != nil {
					return moved, err
				}
				continue
			}
			fmt.Printf("[PLATFORM_SCRIPTS_MIGRATION] kept existing %s; the old copy is in the backup\n", to)
			if err := os.RemoveAll(from); err != nil {
				return moved, err
			}
			continue
		}
		if err := moveEntry(from, to); err != nil {
			return moved, fmt.Errorf("move %s: %w", from, err)
		}
		moved++
	}
	return moved, os.Remove(source)
}

// removeEmptyUp removes now-empty parents of dir up to (not including) stop, so code/ goes when nothing else is in it.
func removeEmptyUp(dir, stop string) {
	for d := filepath.Dir(dir); d != stop && Within(stop, d); d = filepath.Dir(d) {
		if os.Remove(d) != nil {
			return
		}
	}
}

// rewritePlatformScriptPaths replaces old script paths in the text files under dir (rendered pages, saved records).
func rewritePlatformScriptPaths(dir, backup string) (int, error) {
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return 0, nil
	}
	changed := 0
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != dir && (d.Name() == "runs" || d.Name() == "node_modules" || d.Name() == ".git") {
				return filepath.SkipDir
			}
			return nil
		}
		switch strings.ToLower(filepath.Ext(p)) {
		case ".html", ".htm", ".js", ".mjs", ".json", ".tsx", ".ts", ".jsx", ".css":
		default:
			return nil
		}
		info, err := d.Info()
		if err != nil || !info.Mode().IsRegular() || info.Size() > 32<<20 {
			return err
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		next := raw
		for _, r := range platformScriptRewrites {
			next = bytes.ReplaceAll(next, []byte(r.old), []byte(r.new))
		}
		if bytes.Equal(next, raw) {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		saved := filepath.Join(backup, "rewritten", filepath.Base(dir), rel)
		if err := os.MkdirAll(filepath.Dir(saved), 0o700); err != nil {
			return err
		}
		if err := copyPrivateEntry(p, saved); err != nil {
			return err
		}
		if err := os.WriteFile(p, next, info.Mode().Perm()); err != nil {
			return err
		}
		changed++
		return nil
	})
	return changed, err
}

func containsString(list []string, s string) bool {
	for _, item := range list {
		if item == s {
			return true
		}
	}
	return false
}
