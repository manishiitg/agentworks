package dashboardjson

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/dashboards"
)

var revisionName = regexp.MustCompile(`^[a-f0-9]{64}\.json$`)
var automaticMarker = regexp.MustCompile(`<!-- agentworks-default-dashboard:([a-f0-9]{16}) -->\n$`)

type change struct {
	path   string
	body   []byte
	mode   fs.FileMode
	remove bool
}

// Run converts only saved managed JSON revisions, before the workspace starts.
// IDs, saved revision paths, draft/published pointers, history, sharing, grants,
// scripts, data and project settings stay in place. A private backup holds the
// exact original bytes. No dashboard code, query, script or function is run.
func checkedPath(docs, path string) error {
	rel, err := filepath.Rel(docs, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return fmt.Errorf("dashboard migration path leaves workspace")
	}
	for p := path; p != docs; p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("dashboard migration does not follow symlinks: %s", p)
		}
	}
	return nil
}

func Run(docs string) (int, string, error) {
	docs, err := filepath.Abs(docs)
	if err != nil {
		return 0, "", err
	}
	var changes []change
	count := 0
	err = filepath.WalkDir(docs, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if entry.IsDir() {
			if entry.Name() == "node_modules" || entry.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !revisionName.MatchString(entry.Name()) || !strings.Contains(filepath.ToSlash(path), "/db/reports/.dashboard-state/") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var snap dashboards.Snapshot
		if err = json.Unmarshal(raw, &snap); err != nil {
			return fmt.Errorf("dashboard migration: invalid saved revision %s: %w", path, err)
		}
		source, ok := snap.Files["index.json"]
		if !ok {
			return nil
		}
		tsx, err := Convert(source)
		if err != nil {
			return fmt.Errorf("dashboard migration %s: %w", path, err)
		}
		oldHTML := snap.Files["index.html"]
		delete(snap.Files, "index.json")
		snap.Files["index.tsx"] = tsx
		snap.Files, err = dashboards.CompileTSX(snap.Title, snap.Files)
		if err != nil {
			return fmt.Errorf("dashboard migration %s: %w", path, err)
		}
		marker := automaticMarker.FindStringSubmatchIndex(oldHTML)
		if marker != nil {
			sum := sha256.Sum256([]byte(oldHTML[:marker[0]]))
			if hex.EncodeToString(sum[:8]) == oldHTML[marker[2]:marker[3]] {
				body := snap.Files["index.html"]
				sum = sha256.Sum256([]byte(body))
				snap.Files["index.html"] = body + "<!-- agentworks-default-dashboard:" + hex.EncodeToString(sum[:8]) + " -->\n"
			}
		}
		body, err := json.Marshal(snap)
		if err != nil {
			return err
		}
		if len(body) > dashboards.MaxBytes {
			return fmt.Errorf("dashboard migration exceeds source limit: %s", path)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		id := filepath.Base(filepath.Dir(path))
		rev := strings.TrimSuffix(entry.Name(), ".json")
		reports := filepath.Dir(filepath.Dir(filepath.Dir(path)))
		render := filepath.Join(reports, "managed", id, rev)
		// Only the changed/generated browser files are written. Private scripts and
		// binary assets are already present and retain their exact ownership/modes.
		for _, name := range []string{"index.tsx", "index.html", "dashboard-app.js", "dashboard-app.css"} {
			if value, ok := snap.Files[name]; ok {
				value = strings.ReplaceAll(value, "{{dashboard_assets}}", "db/reports/managed/"+id+"/"+rev)
				value = strings.ReplaceAll(value, "{{dashboard_scripts}}", "code/reports/managed/"+id+"/"+rev)
				changes = append(changes, change{filepath.Join(render, name), []byte(value), 0640, false})
			}
		}
		changes = append(changes, change{path: filepath.Join(render, "index.json"), remove: true})
		changes = append(changes, change{path, body, info.Mode().Perm(), false})
		count++
		return nil
	})
	if err != nil || count == 0 {
		return 0, "", err
	}
	backup := filepath.Join(filepath.Dir(docs), ".dashboard-json-backups", time.Now().UTC().Format("20060102T150405.000000000Z"))
	// Back up all replaced/deleted bytes before the first mutation.
	for _, change := range changes {
		if err := checkedPath(docs, change.path); err != nil {
			return 0, backup, err
		}
	}
	for _, change := range changes {
		original, err := os.ReadFile(change.path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return 0, backup, err
		}
		rel, err := filepath.Rel(docs, change.path)
		if err != nil {
			return 0, backup, err
		}
		target := filepath.Join(backup, rel)
		if err = os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return 0, backup, err
		}
		if err = os.WriteFile(target, original, 0600); err != nil {
			return 0, backup, err
		}
	}
	for _, change := range changes {
		if change.remove {
			if err = os.Remove(change.path); err != nil && !os.IsNotExist(err) {
				return 0, backup, err
			}
			continue
		}
		if err = os.MkdirAll(filepath.Dir(change.path), 0770); err != nil {
			return 0, backup, err
		}
		mode := change.mode
		if info, statErr := os.Stat(change.path); statErr == nil {
			mode = info.Mode().Perm()
		}
		// Existing file ownership and access bits remain; these are the same files,
		// not a second source registry or a new publication/access boundary.
		if err = os.WriteFile(change.path, change.body, mode); err != nil {
			return 0, backup, err
		}
	}
	return count, backup, nil
}
