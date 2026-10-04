// Package workflowkb resolves host-local workflow KB references from canonical
// manifests. It has no agent, HTTP, or session dependencies.
package workflowkb

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
)

type Manifest struct {
	KnowledgebaseMode string `json:"knowledgebase_mode"`
	ID                string `json:"id"`
	Label             string `json:"label"`
	CreatedBy         string `json:"created_by"`
	Access            *struct {
		Owners           []string `json:"owners"`
		Readers          []string `json:"readers"`
		AllowedKBWriters []string `json:"allowed_kb_writers"`
	} `json:"access"`
	Sources []workflowtypes.KnowledgebaseSource `json:"knowledgebase_sources"`
}

type ResolvedSource struct {
	workflowtypes.KnowledgebaseSource
	Label         string `json:"label,omitempty"`
	WorkspacePath string `json:"workspace_path,omitempty"`
	Path          string `json:"-"`
	Available     bool   `json:"available"`
	Reason        string `json:"reason,omitempty"`
}

func Within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func ReadManifest(root, workspace string) (Manifest, error) {
	var m Manifest
	if filepath.IsAbs(workspace) {
		return m, fmt.Errorf("workflow path must be relative")
	}
	target := filepath.Join(root, filepath.FromSlash(workspace), "workflow.json")
	canonical, err := filepath.EvalSymlinks(target)
	if err != nil {
		return m, err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return m, err
	}
	if !Within(filepath.Join(root, "Workflow"), canonical) {
		return m, fmt.Errorf("workflow is outside this host's Workflow directory")
	}
	raw, err := os.ReadFile(canonical)
	if err != nil {
		return m, err
	}
	err = json.Unmarshal(raw, &m)
	return m, err
}

func owners(m Manifest) []string {
	if m.Access != nil && len(m.Access.Owners) > 0 {
		return m.Access.Owners
	}
	if m.CreatedBy != "" {
		return []string{m.CreatedBy}
	}
	return nil
}

// A workflow's full audience must be allowed to read the source. Scheduled runs
// have no interactive user, and consumer outputs are visible to its readers.
// This also makes raw JSON references fail closed after ownership changes.
func AudienceCanRead(consumer, source Manifest) bool {
	allowed := owners(source)
	if len(allowed) == 0 {
		return true
	} // legacy, account-visible source
	audience := append([]string{}, owners(consumer)...)
	if len(audience) == 0 {
		return false
	} // an account-visible consumer would widen access
	if consumer.Access != nil {
		audience = append(audience, consumer.Access.Readers...)
	}
	if source.Access != nil {
		allowed = append(append([]string{}, allowed...), source.Access.Readers...)
	}
	for _, id := range audience {
		found := false
		for _, other := range allowed {
			if id == other {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// AudienceCanWrite requires everything AudienceCanRead requires (the
// consumer's full audience must already be allowed to read the source) PLUS
// an explicit opt-in from the source: the consumer's workflow ID must be
// named in source.Access.AllowedKBWriters. Audience overlap alone is not
// consent to mutate another workflow's KB -- unlike the legacy "no owners
// recorded" case in AudienceCanRead, a source with no access block, or one
// that has not named this consumer, grants no write access at all.
func AudienceCanWrite(consumer, source Manifest) bool {
	if !AudienceCanRead(consumer, source) {
		return false
	}
	if source.Access == nil || strings.TrimSpace(consumer.ID) == "" {
		return false
	}
	for _, id := range source.Access.AllowedKBWriters {
		if id == consumer.ID {
			return true
		}
	}
	return false
}

// Discover is a host-local ID registry over workflow manifests. Workflow folder
// names may change; duplicate IDs fail resolution instead of selecting one.
func Discover(root string) (map[string][]string, error) {
	result := map[string][]string{}
	start := filepath.Join(root, "Workflow")
	err := filepath.WalkDir(start, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		if path == start {
			return nil
		}
		raw, err := os.ReadFile(filepath.Join(path, "workflow.json"))
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		var m Manifest
		if json.Unmarshal(raw, &m) == nil && m.ID != "" {
			rel, _ := filepath.Rel(root, path)
			result[m.ID] = append(result[m.ID], filepath.ToSlash(rel))
		}
		return filepath.SkipDir
	})
	return result, err
}

func Resolve(root, workspace string, sources []workflowtypes.KnowledgebaseSource) ([]ResolvedSource, error) {
	consumer, err := ReadManifest(root, workspace)
	if err != nil {
		return nil, err
	}
	if sources == nil {
		sources = consumer.Sources
	}
	if err := workflowtypes.ValidateKnowledgebaseSources(sources, consumer.ID); err != nil {
		return nil, err
	}
	result := make([]ResolvedSource, 0, len(sources))
	if len(sources) == 0 {
		return result, nil
	}
	registry, err := Discover(root)
	if err != nil {
		return nil, err
	}
	for _, ref := range sources {
		item := ResolvedSource{KnowledgebaseSource: ref}
		paths := registry[ref.WorkflowID]
		if len(paths) != 1 {
			item.Reason = "Source workflow is unavailable on this host or its ID is ambiguous"
			result = append(result, item)
			continue
		}
		source, err := ReadManifest(root, paths[0])
		if err != nil {
			item.Reason = "Source knowledge is not readable by every member of this workflow"
			result = append(result, item)
			continue
		}
		if ref.Access == "write" {
			if !AudienceCanWrite(consumer, source) {
				item.Reason = "Source workflow has not granted this workflow write access to its knowledgebase"
				result = append(result, item)
				continue
			}
		} else if !AudienceCanRead(consumer, source) {
			item.Reason = "Source knowledge is not readable by every member of this workflow"
			result = append(result, item)
			continue
		}
		if source.KnowledgebaseMode == "shared" {
			item.Reason = "Source migrated to shared Knowledge Base; its owner must replace this alias with an authorized MCP binding"
			result = append(result, item)
			continue
		}
		item.Label = source.Label
		item.WorkspacePath = paths[0]
		kb := filepath.Join(root, paths[0], "knowledgebase")
		canonical, err := filepath.EvalSymlinks(kb)
		sourceRoot, rootErr := filepath.EvalSymlinks(filepath.Join(root, paths[0]))
		if err != nil || rootErr != nil || canonical != filepath.Join(sourceRoot, "knowledgebase") {
			item.Reason = "Source knowledgebase is missing or redirects outside its canonical folder"
			result = append(result, item)
			continue
		}
		info, err := os.Stat(canonical)
		if err != nil || !info.IsDir() {
			item.Reason = "Source knowledgebase is not a directory"
			result = append(result, item)
			continue
		}
		err = filepath.WalkDir(canonical, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.Type()&os.ModeSymlink != 0 {
				target, err := filepath.EvalSymlinks(path)
				if err != nil || !Within(canonical, target) {
					return fmt.Errorf("KB contains an unavailable or escaping symbolic link")
				}
			}
			return nil
		})
		if err != nil {
			item.Reason = "Source KB contains an unreadable path or escaping symbolic link"
		} else {
			item.Path = canonical
			item.Available = true
		}
		result = append(result, item)
	}
	return result, nil
}

func Validate(root, workspace string, sources []workflowtypes.KnowledgebaseSource) error {
	if sources == nil {
		sources = []workflowtypes.KnowledgebaseSource{}
	}
	resolved, err := Resolve(root, workspace, sources)
	if err != nil {
		return err
	}
	for _, item := range resolved {
		if !item.Available {
			return fmt.Errorf("KB source %q: %s", item.Alias, item.Reason)
		}
	}
	return nil
}

// ReadFile confines UI reads to the already resolved, attached KB.
func ReadFile(source ResolvedSource, relative string) ([]byte, error) {
	if !source.Available || relative == "" || filepath.IsAbs(relative) {
		return nil, fmt.Errorf("unavailable source or invalid KB file")
	}
	root, err := os.OpenRoot(source.Path)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	file, err := root.Open(filepath.FromSlash(relative))
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	const maxSize = 2 * 1024 * 1024
	if !info.Mode().IsRegular() || info.Size() > maxSize {
		return nil, fmt.Errorf("KB viewer accepts regular files up to 2 MB")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxSize+1))
	if len(data) > maxSize {
		return nil, fmt.Errorf("KB file exceeds 2 MB")
	}
	return data, err
}

// Candidates lists authorized source IDs without exposing their contents or host
// paths. Builder configuration discovery uses this instead of guessed IDs.
func Candidates(root, workspace string) ([]ResolvedSource, error) {
	consumer, err := ReadManifest(root, workspace)
	if err != nil {
		return nil, err
	}
	registry, err := Discover(root)
	if err != nil {
		return nil, err
	}
	result := []ResolvedSource{}
	for id, paths := range registry {
		if id == consumer.ID || len(paths) != 1 {
			continue
		}
		m, err := ReadManifest(root, paths[0])
		if err != nil || !AudienceCanRead(consumer, m) {
			continue
		}
		info, err := os.Stat(filepath.Join(root, paths[0], "knowledgebase"))
		if err != nil || !info.IsDir() {
			continue
		}
		result = append(result, ResolvedSource{KnowledgebaseSource: workflowtypes.KnowledgebaseSource{WorkflowID: id, Access: "read"}, Label: m.Label, WorkspacePath: paths[0]})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].WorkflowID < result[j].WorkflowID })
	return result, nil
}
