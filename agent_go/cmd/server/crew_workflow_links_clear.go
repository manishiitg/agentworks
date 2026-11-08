package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
)

// One-off removal of the workflows a Crew had linked (owner, 7 Nov: Crews stop linking workflows; a Crew uses a
// workflow through its functions or by running it, and keeps linking other Crews). Run once per server, before
// anything serves a turn. It takes every "Workflow/..." path out of workflow_context_paths in each Crew's manifests
// and in the Crew chats of each person's conversation registry, and nothing else in them: Crew links, the rest of
// the files and every standalone workflow's own links stay. The original is copied beside it first and a done marker
// is written last. Remove this file once every server has started on this version.

const (
	crewWorkflowLinksClearMarker = "_system/crew-workflow-links-clear.done"
	crewWorkflowLinksClearBackup = ".before-workflow-links-clear"
)

func clearCrewWorkflowLinks() error {
	docs := fsutil.WorkspaceDocsRoot()
	marker := filepath.Join(docs, filepath.FromSlash(crewWorkflowLinksClearMarker))
	if _, err := os.Stat(marker); err == nil {
		return nil
	}
	var failures []error
	for _, file := range crewLinkFiles(docs) {
		removed, err := clearWorkflowLinksInFile(file, false)
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", file, err))
		} else if removed > 0 {
			log.Printf("[CREW_LINKS] event=workflow_links_cleared file=%s removed=%d", filepath.Base(file), removed)
		}
	}
	for _, file := range crewRegistryFiles(docs) {
		removed, err := clearWorkflowLinksInFile(file, true)
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", file, err))
		} else if removed > 0 {
			log.Printf("[CREW_LINKS] event=workflow_links_cleared file=%s removed=%d", filepath.Base(file), removed)
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

// crewLinkFiles lists each Crew's workflow.json and product.json, in the Crew folder and in its private data.
func crewLinkFiles(docs string) []string {
	var files []string
	for _, pattern := range []string{
		filepath.Join("Crew", "*"), filepath.Join("_system", "crew-data", "*"),
		filepath.Join("_users", "*", "Crew", "*"), filepath.Join("_users", "*", "_system", "crew-data", "*"),
	} {
		dirs, _ := filepath.Glob(filepath.Join(docs, pattern))
		for _, dir := range dirs {
			for _, name := range []string{"workflow.json", "product.json"} {
				if info, err := os.Stat(filepath.Join(dir, name)); err == nil && info.Mode().IsRegular() {
					files = append(files, filepath.Join(dir, name))
				}
			}
		}
	}
	return files
}

func crewRegistryFiles(docs string) []string {
	var files []string
	people, _ := filepath.Glob(filepath.Join(docs, "_users", "*"))
	for _, person := range people {
		file := filepath.Join(docs, filepath.FromSlash(productConversationRegistryPath(filepath.Base(person))))
		if info, err := os.Stat(file); err == nil && info.Mode().IsRegular() {
			files = append(files, file)
		}
	}
	return files
}

func isWorkflowLink(value any) bool {
	path, _ := value.(string)
	return strings.HasPrefix(strings.Trim(strings.TrimSpace(path), "/"), "Workflow/")
}

// dropWorkflowLinks removes the workflow links of one object's workflow_context_paths (top level and under
// capabilities) and returns how many it removed.
func dropWorkflowLinks(object map[string]any) int {
	removed := 0
	trim := func(holder map[string]any) {
		paths, ok := holder["workflow_context_paths"].([]any)
		if !ok {
			return
		}
		kept := make([]any, 0, len(paths))
		for _, path := range paths {
			if isWorkflowLink(path) {
				removed++
				continue
			}
			kept = append(kept, path)
		}
		holder["workflow_context_paths"] = kept
	}
	trim(object)
	if capabilities, _ := object["capabilities"].(map[string]any); capabilities != nil {
		trim(capabilities)
	}
	return removed
}

// clearWorkflowLinksInFile edits one manifest, or (registry) the Crew chats of one conversation registry. A file
// with no workflow link is left untouched.
func clearWorkflowLinksInFile(file string, registry bool) (int, error) {
	raw, err := os.ReadFile(file)
	if err != nil {
		return 0, err
	}
	if !bytes.Contains(raw, []byte(`"Workflow/`)) && !bytes.Contains(raw, []byte(`"/Workflow/`)) {
		return 0, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var document map[string]any
	if err := decoder.Decode(&document); err != nil {
		return 0, err
	}
	removed := 0
	if !registry {
		removed = dropWorkflowLinks(document)
	} else {
		for _, section := range []string{"entries", "previous"} {
			groups, _ := document[section].(map[string]any)
			for _, group := range groups {
				items, isList := group.([]any)
				if !isList {
					items = []any{group}
				}
				for _, item := range items {
					if entry, _ := item.(map[string]any); entry != nil && entry["profile_id"] == crewProfileID {
						removed += dropWorkflowLinks(entry)
					}
				}
			}
		}
	}
	if removed == 0 {
		return 0, nil
	}
	info, err := os.Stat(file)
	if err != nil {
		return 0, err
	}
	// Keep the first copy if an earlier start stopped part-way.
	if backup, err := os.OpenFile(file+crewWorkflowLinksClearBackup, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600); err == nil {
		_, writeErr := backup.Write(raw)
		if err := errors.Join(writeErr, backup.Close()); err != nil {
			return 0, fmt.Errorf("backup: %w", err)
		}
	} else if !os.IsExist(err) {
		return 0, fmt.Errorf("backup: %w", err)
	}
	if err := fsutil.WriteJSONAtomic(file, document, info.Mode().Perm()); err != nil {
		return 0, err
	}
	return removed, nil
}
