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
func projectStorageResumeRuntime(agent *mcpagent.Agent, runtime *ChatHistoryAgentRuntime) *ChatHistoryAgentRuntime {
	if agent == nil || runtime == nil || runtime.AgentSessionHandle == nil {
		return runtime
	}
	current := mcpagent.SnapshotAgentSession(agent)
	if current == nil {
		return runtime
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
		return runtime
	}
	active, ok := resolveCrewPath(context.Background(), "", rel)
	if !ok {
		return runtime
	}
	product, _, known := projectProductForPath(active.Root)
	if !known || product.ProfileID != "code" {
		return runtime
	}
	// Do not partially adopt a handle whose primary cwd belongs elsewhere.
	savedRel, savedOK := relative(runtime.AgentSessionHandle.Provider.WorkingDir)
	if !savedOK {
		return runtime
	}
	savedRef, savedOK := resolveCrewPath(context.Background(), active.OwnerID, savedRel)
	if !savedOK || savedRef.Root != active.Root || savedRef.OwnerID != active.OwnerID {
		return runtime
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
		return runtime
	}
	copyRuntime.AgentSessionHandle = &copyHandle
	return &copyRuntime
}
