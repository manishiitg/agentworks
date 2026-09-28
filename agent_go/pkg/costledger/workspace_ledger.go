package costledger

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/remoteplacement"
)

// WorkspaceCostsRelativePath is the path, relative to a workflow's own
// workspace folder, where that workflow's own cost ledger lives. It
// deliberately matches the shape of the workflow's other durable stores
// (db/db.sqlite, knowledgebase/) so it needs no new folder-guard grant --
// existing per-workflow read scopes already cover it.
const WorkspaceCostsRelativePath = "costs/costs.sqlite"

var (
	workspaceLedgersMu sync.Mutex
	workspaceLedgers   = make(map[string]*Ledger) // absolute costs.sqlite path -> open ledger
)

// workspaceCostsPathPrefix is the only workspace-path shape this ledger
// applies to. Cost events can also be attributed to non-workflow paths
// (plain chat sessions, per-user Chats folders) -- those have no workflow
// agent that could ever read a per-workspace ledger back, since Pulse and
// the Workflow Builder only ever run scoped to a "Workflow/<name>" folder,
// so writing one for them would just be a stray, unused file.
const workspaceCostsPathPrefix = "Workflow/"

// WorkspaceLedgerPath resolves the absolute costs.sqlite path for a
// workflow's own workspace (e.g. "Workflow/social-media"), relative to the
// workspace-docs root. Returns "" for an empty, non-workflow, or otherwise
// unscopable workspace path (nothing to scope a ledger to).
func WorkspaceLedgerPath(workspacePath string) string {
	workspacePath = strings.Trim(strings.TrimSpace(workspacePath), "/")
	if workspacePath == "" || !strings.HasPrefix(workspacePath, workspaceCostsPathPrefix) {
		return ""
	}
	// A workflow on a remote workspace server has no local folder; opening
	// the ledger here would create a local split-brain copy. Its costs stay
	// in the global ledger until the server grows a cost-store endpoint.
	if remoteplacement.IsRemote(fsutil.WorkspaceDocsRoot(), workspacePath) {
		return ""
	}
	return filepath.Join(fsutil.WorkspaceDocsRoot(), workspacePath, WorkspaceCostsRelativePath)
}

// WorkspaceLedger returns the open per-workspace cost ledger for
// workspacePath, opening it on first use and reusing the same connection
// pool for every later call with the same path. This is the write side of
// PLAT-184: every cost event attributable to one workflow is also recorded
// here, alongside the existing global ledger, so that workflow's own agents
// (Pulse, the Workflow Builder) can read their own cost data through their
// normal folder-guard-scoped access -- the global ledger sits outside every
// workflow's own folder and is not reachable by any agent at all.
//
// Returns (nil, nil) for an empty workspacePath: there is nothing to scope
// a ledger to (e.g. plain chat, or a workflow-builder session before any
// workflow folder has been chosen). Callers must treat a nil ledger as
// "nothing to do here", not an error.
func WorkspaceLedger(workspacePath string) (*Ledger, error) {
	path := WorkspaceLedgerPath(workspacePath)
	if path == "" {
		return nil, nil
	}

	workspaceLedgersMu.Lock()
	defer workspaceLedgersMu.Unlock()

	if ledger, ok := workspaceLedgers[path]; ok {
		return ledger, nil
	}
	ledger, err := NewSQLiteLedger(path)
	if err != nil {
		return nil, fmt.Errorf("costledger: open workspace ledger %s: %w", path, err)
	}
	workspaceLedgers[path] = ledger
	return ledger, nil
}
