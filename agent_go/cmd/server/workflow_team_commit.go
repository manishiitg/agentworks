package server

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	workspacepkg "github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
)

// Native tools prepare a bounded edit in memory. Authorization of the complete
// change and revision validation happen under one short commit lock, never a
// conversation lock. Failed preparation does not leave a half-updated plan.
func stageTeamWrite(ctx context.Context, path, content string) (bool, error) {
	r, ok := ctx.Value(teamPlanReceiptKey{}).(*teamPlanReceipt)
	if !ok {
		return false, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.staging {
		return false, nil
	}
	root, _ := teamWorkspacePath(path)
	if root != r.root {
		return true, fmt.Errorf("team plan tools cannot write another workspace")
	}
	if len(content) > 8<<20 {
		return true, fmt.Errorf("team edit exceeds size limit")
	}
	r.pending[path] = content
	return true, nil
}
func stagedTeamRead(ctx context.Context, path string) (string, bool, bool) {
	r, ok := ctx.Value(teamPlanReceiptKey{}).(*teamPlanReceipt)
	if !ok {
		return "", false, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	content, found := r.pending[path]
	return content, found, found
}
func prepareTeamEdit(ctx context.Context, root, revision string) context.Context {
	ctx = withTeamPlanReceipt(ctx)
	r := ctx.Value(teamPlanReceiptKey{}).(*teamPlanReceipt)
	r.root = root
	r.expectedPlanRevision = revision
	r.staging = true
	r.pending = map[string]string{}
	return ctx
}
func commitTeamEdit(ctx context.Context, result string) error {
	r := ctx.Value(teamPlanReceiptKey{}).(*teamPlanReceipt)
	var status map[string]interface{}
	if json.Unmarshal([]byte(result), &status) == nil && status["success"] == false {
		return fmt.Errorf("plan tool refused edit: %s", result)
	}
	r.mu.Lock()
	r.staging = false
	pending := r.pending
	r.mu.Unlock()
	defer func() { r.mu.Lock(); r.pending = nil; r.mu.Unlock() }()
	if len(pending) == 0 {
		return nil
	}
	release, err := teamLock(ctx, r.root)
	if err != nil {
		return err
	}
	defer release()
	ctx = context.WithValue(ctx, teamLockHeldKey{}, r.root)
	baselineCtx := context.WithValue(ctx, teamPlanReceiptKey{}, nil)
	paths := make([]string, 0, len(pending))
	for p := range pending {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	previous := map[string]string{}
	exists := map[string]bool{}
	// Validate every file before the first write, against the latest policy.
	for _, p := range paths {
		raw, found, e := readFileFromWorkspace(baselineCtx, p)
		if e != nil {
			return e
		}
		previous[p] = raw
		exists[p] = found
		unlock, e := teamManagedWrite(ctx, p, pending[p])
		if e != nil {
			return e
		}
		unlock()
	}
	// The lock prevents other managed writers from slipping between validation
	// and replacement. Unexpected transport failures restore completed writes;
	// report any uncertain rollback instead of treating it as a successful edit.
	commitCtx := context.WithValue(ctx, teamCommitPermitKey{}, true)
	written := []string{}
	for _, p := range paths {
		if e := writeFileToWorkspace(commitCtx, p, pending[p]); e != nil {
			rollback := []string{}
			for i := len(written) - 1; i >= 0; i-- {
				q := written[i]
				var restore error
				if exists[q] {
					restore = writeFileToWorkspace(commitCtx, q, previous[q])
				} else {
					restore = deleteWorkspaceFile(commitCtx, q)
				}
				if restore != nil {
					rollback = append(rollback, q)
				}
			}
			if len(rollback) > 0 {
				return fmt.Errorf("edit failed and rollback requires reconciliation for %s: %w", strings.Join(rollback, ", "), e)
			}
			return fmt.Errorf("edit failed and completed writes were restored: %w", e)
		}
		written = append(written, p)
	}
	return nil
}
func validateTeamQuery(ctx context.Context, req QueryRequest) error {
	if req.SelectedFolder == "" {
		return nil
	}
	m, ok, e := ReadWorkflowManifest(ctx, req.SelectedFolder)
	if e != nil {
		return e
	}
	if !ok || m.Team == nil {
		return nil
	}
	actor := teamActor(ctx)
	if actor == m.Team.LeaderID {
		return nil
	}
	if req.AgentMode == "workflow" || req.AgentMode == "workflow_phase" && req.PhaseID != "workflow-builder" {
		return fmt.Errorf("whole-workflow execution requires the Leader; use Builder execute_step for your authorized route")
	}
	return nil
}
func init() {
	workspacepkg.FileReadOverlay = stagedTeamRead
	workspacepkg.FileWriteInterceptor = stageTeamWrite
}
