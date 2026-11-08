package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// One-off rename of a function's saved "fresh" to "new" (a new conversation for every call), so one spelling means
// that everywhere: a schedule, a webhook and a function. Run once per server before anything can call a function.
// It rewrites functions.json at each Crew's root, in each workflow's own runtime and definitions folders, and in the
// published copies of Crews; the original is copied beside it first and a done marker is written last. Remove this
// file once every server has started on this version.

const (
	crewFunctionNewRenameMarker = "_system/function-new-rename.done"
	crewFunctionNewRenameBackup = ".before-function-new-rename"
)

func renameFunctionFreshToNew() error {
	docs := fsutil.WorkspaceDocsRoot()
	marker := filepath.Join(docs, filepath.FromSlash(crewFunctionNewRenameMarker))
	if _, err := os.Stat(marker); err == nil {
		return nil
	}
	var failures []error
	for _, file := range functionFilesToRename(docs, &failures) {
		changed, err := renameFunctionFreshInFile(file)
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", file, err))
			continue
		}
		if changed > 0 {
			log.Printf("[CREW_FUNCTIONS] event=fresh_renamed_to_new file=%s functions=%d backup=%s", filepath.Base(filepath.Dir(file)), changed, filepath.Base(file)+crewFunctionNewRenameBackup)
		}
	}
	if err := errors.Join(failures...); err != nil {
		return err // no marker: the next start tries again
	}
	if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
		return err
	}
	return os.WriteFile(marker, []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o600)
}

// functionFilesToRename lists the functions.json files that exist: each Crew's own, each workflow's, and the
// published copies.
func functionFilesToRename(docs string, failures *[]error) []string {
	var files []string
	add := func(file string) {
		if info, err := os.Stat(file); err == nil && info.Mode().IsRegular() {
			files = append(files, file)
		}
	}
	if records, err := defaultProjectOwners().All(); err != nil {
		*failures = append(*failures, fmt.Errorf("list Crews: %w", err))
	} else {
		for _, rec := range records {
			if rec.Product == crewProfileID {
				add(filepath.Join(docs, filepath.FromSlash(projectStorageRoot(rec)), crewFunctionsFileName))
			}
		}
	}
	if layouts, err := crewroot.ExecutableLayouts(docs); err != nil {
		*failures = append(*failures, fmt.Errorf("list workflows: %w", err))
	} else {
		for _, layout := range layouts {
			add(filepath.Join(layout.Runtime, crewFunctionsFileName))
			add(filepath.Join(layout.Definitions, crewFunctionsFileName))
		}
	}
	published, _ := filepath.Glob(filepath.Join(docs, "_users", "*", "crew-published", "*", "*", "*", "source", crewFunctionsFileName))
	for _, file := range published {
		add(file)
	}
	return files
}

// renameFunctionFreshInFile turns "fresh" into "new" in the conversation of each function's action and in its
// execution key, and nothing else. It returns how many functions changed; a file with none is left untouched.
func renameFunctionFreshInFile(file string) (int, error) {
	raw, err := os.ReadFile(file)
	if err != nil {
		return 0, err
	}
	if !bytes.Contains(raw, []byte(`"fresh"`)) {
		return 0, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var document map[string]any
	if err := decoder.Decode(&document); err != nil {
		return 0, err
	}
	functions, _ := document["functions"].([]any)
	changed := 0
	for _, item := range functions {
		function, _ := item.(map[string]any)
		if function == nil {
			continue
		}
		touched := false
		if action, _ := function["action"].(map[string]any); action != nil && action["conversation"] == "fresh" {
			action["conversation"], touched = runDestinationNew, true
		}
		if function["execution"] == "fresh" {
			function["execution"], touched = runDestinationNew, true
		}
		if touched {
			changed++
		}
	}
	if changed == 0 {
		return 0, nil
	}
	info, err := os.Stat(file)
	if err != nil {
		return 0, err
	}
	backup, err := os.OpenFile(file+crewFunctionNewRenameBackup, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return 0, fmt.Errorf("backup: %w", err)
	}
	_, writeErr := backup.Write(raw)
	if err := errors.Join(writeErr, backup.Close()); err != nil {
		return 0, fmt.Errorf("backup: %w", err)
	}
	if err := fsutil.WriteJSONAtomic(file, document, info.Mode().Perm()); err != nil {
		return 0, err
	}
	return changed, nil
}
