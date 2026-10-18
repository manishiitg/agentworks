package server

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

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
	if _, replacing := r.pending[path]; !replacing && len(r.pending) >= 128 {
		return true, fmt.Errorf("team edit exceeds file limit")
	}
	total := len(content)
	for p, previous := range r.pending {
		if p != path {
			total += len(previous)
		}
	}
	if total > 32<<20 {
		return true, fmt.Errorf("team edit exceeds total size limit")
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
	return commitTeamEditWithFiles(ctx, result, readFileFromWorkspace, writeFileToWorkspace, deleteWorkspaceFile)
}

// Direct transports supply an anchored local writer while retaining exactly
// the native staging, current team policy and rollback validation. Calling the
// HTTP document writer while this root lock is held would re-enter its lock.
// A validation refusal before the first writer is a known failed operation.
// Once a writer was attempted, transports must retain reconciliation semantics.
type teamCommitBeforeWriteError struct{ error }

func (e *teamCommitBeforeWriteError) Unwrap() error { return e.error }

func commitTeamEditWithFiles(ctx context.Context, result string,
	read func(context.Context, string) (string, bool, error),
	write func(context.Context, string, string) error,
	remove func(context.Context, string) error) (commitErr error) {
	writeAttempted := false
	defer func() {
		if commitErr != nil && !writeAttempted {
			commitErr = &teamCommitBeforeWriteError{commitErr}
		}
	}()
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
	if migrating, _ := ctx.Value(workflowSkillsEditKey{}).(bool); migrating {
		// Recheck typed migration authority inside the commit lock, including
		// migrations with no plan changes and only an audit manifest to save.
		raw, found, err := read(baselineCtx, r.root+"/workflow.json")
		var current WorkflowManifest
		if err != nil || !found || json.Unmarshal([]byte(raw), &current) != nil {
			return fmt.Errorf("workflow settings changed; reread before migrating guides")
		}
		actor := teamActor(ctx)
		if actor == "" || !externalFileAuthoringAllowed(&UserClaims{UserID: actor}, &current) || current.Team != nil && current.Team.LeaderID != actor {
			return fmt.Errorf("ask the current workflow Builder to migrate its guides")
		}
		access := userAccessForClaims(&UserClaims{UserID: actor})
		if access.Disabled || !access.CanEdit || IsMultiUserMode() && !access.Known {
			return fmt.Errorf("account cannot edit this workflow")
		}
	}
	paths := make([]string, 0, len(pending))
	for p := range pending {
		paths = append(paths, p)
	}
	sort.Slice(paths, func(i, j int) bool {
		if migrating, _ := ctx.Value(workflowSkillsEditKey{}).(bool); migrating {
			if paths[i] == r.root+"/workflow.json" && paths[j] != paths[i] {
				return true
			}
			if paths[j] == r.root+"/workflow.json" {
				return false
			}
		}
		return paths[i] < paths[j]
	})
	ctx = context.WithValue(ctx, teamCommitReadKey{}, read)
	previous := map[string]string{}
	exists := map[string]bool{}
	// Validate every file before the first write, against the latest policy.
	for _, p := range paths {
		raw, found, e := read(baselineCtx, p)
		if e != nil {
			return e
		}
		if migrating, _ := ctx.Value(workflowSkillsEditKey{}).(bool); migrating && !strings.HasPrefix(p, r.root+"/planning/changelog/") && !strings.HasPrefix(p, r.root+"/planning/revisions/") {
			r.mu.Lock()
			baseline, readBefore := r.reads[p]
			r.mu.Unlock()
			if !readBefore || raw != baseline {
				return fmt.Errorf("guide migration source or settings changed; reread and reconcile before retrying")
			}
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
		writeAttempted = true
		if e := write(commitCtx, p, pending[p]); e != nil {
			reconcileCtx, cancel := context.WithTimeout(context.WithoutCancel(commitCtx), 10*time.Second)
			rollback := []string{}
			raw, found, readErr := read(context.WithValue(reconcileCtx, teamPlanReceiptKey{}, nil), p)
			if readErr != nil {
				rollback = append(rollback, p)
			} else if found && raw == pending[p] {
				written = append(written, p)
			} else if found != exists[p] || raw != previous[p] {
				rollback = append(rollback, p)
			}
			for i := len(written) - 1; i >= 0; i-- {
				q := written[i]
				var restore error
				if exists[q] {
					restore = write(reconcileCtx, q, previous[q])
				} else {
					restore = remove(reconcileCtx, q)
				}
				if restore != nil {
					rollback = append(rollback, q)
				}
			}
			cancel()
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
	workspacepkg.BeforePathMutation = teamStructuralMutation
	workspacepkg.FileReadOverlay = stagedTeamRead
	workspacepkg.FileWriteInterceptor = stageTeamWrite
}

func teamStructuralMutation(ctx context.Context, paths ...string) error {
	if trusted, _ := ctx.Value(teamCommitPermitKey{}).(bool); trusted {
		return nil
	}
	for _, p := range paths {
		root, rel := teamWorkspacePath(p)
		if root == "" || !teamProtectedSource(rel) {
			continue
		}
		m, found, e := ReadWorkflowManifest(ctx, root)
		if e != nil {
			return e
		}
		if found && m.Team != nil {
			return fmt.Errorf("team source moves and raw deletions are disabled; use scoped plan/source edits")
		}
	}
	return nil
}

// Manual triggering has run and housekeeping grants, independent of who
// authored the schedule. Standing authorization is still checked at firing.
func authorizeTeamScheduleTrigger(ctx context.Context, root, id string) error {
	m, found, e := ReadWorkflowManifest(ctx, root)
	if e != nil {
		return e
	}
	if !found || m.Team == nil {
		return nil
	}
	actor := teamActor(ctx)
	if actor == m.Team.LeaderID {
		return nil
	}
	member := m.Team.member(actor)
	if member == nil || !member.Run {
		return fmt.Errorf("route run permission is required")
	}
	if a := userAccessForClaims(&UserClaims{UserID: actor}); a.Disabled || !a.Known {
		return fmt.Errorf("account is unavailable")
	}
	var schedule *WorkflowSchedule
	for i := range m.Schedules {
		if m.Schedules[i].ID == id {
			schedule = &m.Schedules[i]
			break
		}
	}
	if schedule == nil {
		return fmt.Errorf("schedule not found")
	}
	// Reuse the route/action checks with a copy: this check does not confer or
	// persist schedule-management permission.
	policy := *m.Team
	policy.Contributors = append([]WorkflowContributor{}, m.Team.Contributors...)
	for i := range policy.Contributors {
		if policy.Contributors[i].UserID == actor {
			policy.Contributors[i].ManageSchedules = true
		}
	}
	return teamScheduleAuthority(&policy, actor, *schedule)
}

func authorizeTeamAction(ctx context.Context, root, action string) error {
	m, found, e := ReadWorkflowManifest(ctx, root)
	if e != nil {
		return e
	}
	if !found || m.Team == nil {
		return nil
	}
	actor := teamActor(ctx)
	access := userAccessForClaims(&UserClaims{UserID: actor})
	if access.Disabled || !access.CanEdit || IsMultiUserMode() && !access.Known {
		return fmt.Errorf("account cannot perform this action")
	}
	if actor == m.Team.LeaderID {
		return nil
	}
	member := m.Team.member(actor)
	if member == nil {
		return fmt.Errorf("no current team membership")
	}
	allowed := action == "backup" && member.Backup || action == "publish" && member.Publish || action == "notify" && member.Notify
	if !allowed {
		return fmt.Errorf("%s requires its own team grant", action)
	}
	return nil
}
