package server

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	mcpagent "github.com/manishiitg/mcpagent/agent"
)

// A direct Code runtime's persisted cwd wins over a newly configured cwd in
// the SDK. Fold only aliases of the already-admitted current project, on a
// copy. Private linked runtimes retain their existing exact mode checks, and
// native IDs, account identities and stored history bytes never change here.
func projectStorageResumeRuntime(agent *mcpagent.Agent, runtime *ChatHistoryAgentRuntime) (*ChatHistoryAgentRuntime, bool) {
	if agent == nil || runtime == nil {
		return runtime, true
	}
	current := mcpagent.SnapshotAgentSession(agent)
	if current == nil {
		return runtime, true
	}
	docs := fsutil.WorkspaceDocsRoot()
	relative := func(dir string) (string, bool) {
		if !filepath.IsAbs(dir) {
			return "", false
		}
		rel, err := filepath.Rel(docs, dir)
		return filepath.ToSlash(rel), err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
	}
	rel, ok := relative(current.Provider.WorkingDir)
	if !ok {
		return runtime, true
	}
	active, ok := resolveCrewPath(context.Background(), "", rel)
	if !ok {
		return runtime, true
	}
	product, _, known := projectProductForPath(active.Root)
	if !known || product.ProfileID != "code" {
		return runtime, true
	}
	// Only relocated projects require this new routing check. Existing legacy
	// and private-runtime restoration keep their own admission rules.
	id, known := projectIdentityOf(active.Root)
	record, registered := defaultProjectOwners().Lookup(id.Product, id.Folder)
	if !known || !registered || record.BindingRoot == "" {
		return runtime, true
	}
	// Do not partially adopt a handle whose primary cwd belongs elsewhere.
	if runtime.AgentSessionHandle == nil {
		return runtime, false
	}
	savedRel, savedOK := relative(runtime.AgentSessionHandle.Provider.WorkingDir)
	if !savedOK {
		return runtime, false
	}
	savedRef, savedOK := resolveCrewPath(context.Background(), active.OwnerID, savedRel)
	if !savedOK || savedRef.Root != active.Root || savedRef.OwnerID != active.OwnerID {
		return runtime, false
	}
	fold := func(dir string) string {
		rel, ok := relative(dir)
		if !ok {
			return dir
		}
		ref, ok := resolveCrewPath(context.Background(), active.OwnerID, rel)
		if !ok || ref.Root != active.Root || ref.OwnerID != active.OwnerID {
			return dir
		}
		return filepath.Join(docs, filepath.FromSlash(ref.Path()))
	}
	copyRuntime, copyHandle := *runtime, *runtime.AgentSessionHandle
	copyHandle.Provider.WorkingDir = fold(copyHandle.Provider.WorkingDir)
	copyHandle.Provider.ProjectDirID = fold(copyHandle.Provider.ProjectDirID)
	copyRuntime.ProjectDirID = fold(copyRuntime.ProjectDirID)
	if copyHandle.Provider.WorkingDir == runtime.AgentSessionHandle.Provider.WorkingDir &&
		copyHandle.Provider.ProjectDirID == runtime.AgentSessionHandle.Provider.ProjectDirID &&
		copyRuntime.ProjectDirID == runtime.ProjectDirID {
		return runtime, true
	}
	copyRuntime.AgentSessionHandle = &copyHandle
	return &copyRuntime, true
}
