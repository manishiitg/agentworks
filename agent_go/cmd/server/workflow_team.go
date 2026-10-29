package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	stepworkflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	workspacepkg "github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

// WorkflowTeam has one canonical owner. Capabilities are not extra roles.
// Route assignments override owner/admin editing privileges.
type WorkflowTeam struct {
	LeaderID        string                    `json:"leader_id"`
	Revision        int                       `json:"revision"`
	Contributors    []WorkflowContributor     `json:"contributors"`
	Routes          []WorkflowRouteAssignment `json:"routes"`
	ReadOnlyStepIDs []string                  `json:"read_only_step_ids,omitempty"`
}
type WorkflowContributor struct {
	UserID          string   `json:"user_id"`
	Run             bool     `json:"run"`
	ManageSchedules bool     `json:"manage_schedules"`
	Approve         bool     `json:"approve"`
	ApprovalIDs     []string `json:"approval_ids,omitempty"`
	Backup          bool     `json:"backup"`
	Publish         bool     `json:"publish"`
	Notify          bool     `json:"notify"`
}
type WorkflowRouteAssignment struct {
	StepID        string `json:"step_id"`
	RouteID       string `json:"route_id"`
	ContributorID string `json:"contributor_id"`
}
type workflowTeamRoute struct {
	StepID  string `json:"step_id"`
	RouteID string `json:"route_id"`
	Name    string `json:"name"`
}

func (t *WorkflowTeam) member(id string) *WorkflowContributor {
	if t != nil {
		for i := range t.Contributors {
			if t.Contributors[i].UserID == id {
				return &t.Contributors[i]
			}
		}
	}
	return nil
}
func teamActor(ctx context.Context) string {
	if c := GetUserFromContext(ctx); c != nil {
		return c.UserID
	}
	id, _ := ctx.Value(common.UserIDKey).(string)
	return id
}
func teamRevision(raw string) string {
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])
}
func teamJSON(raw string) (map[string]interface{}, error) {
	var v map[string]interface{}
	e := json.Unmarshal([]byte(raw), &v)
	if e == nil && v == nil {
		e = fmt.Errorf("expected a JSON object")
	}
	return v, e
}

// Only these schema fields contain steps or route containers. Items and other
// payload objects can also have id/type fields, but never carry step authority.
func teamPlanChild(key string) bool {
	switch key {
	case "steps", "orphan_steps", "predefined_routes", "routes", "sub_agent_step":
		return true
	}
	return false
}

// Routes are identified by parent step and stable route ID, never display name.
func teamWalk(v interface{}, visit func(map[string]interface{})) {
	switch x := v.(type) {
	case map[string]interface{}:
		if _, ok := x["type"]; ok {
			if _, ok = x["id"]; ok {
				visit(x)
			}
		}
		for key, child := range x {
			if teamPlanChild(key) {
				teamWalk(child, visit)
			}
		}
	case []interface{}:
		for _, child := range x {
			teamWalk(child, visit)
		}
	}
}
func teamRoutes(plan map[string]interface{}) []workflowTeamRoute {
	out := []workflowTeamRoute{}
	teamWalk(plan, func(step map[string]interface{}) {
		id, _ := step["id"].(string)
		for _, key := range []string{"predefined_routes", "routes"} {
			rs, _ := step[key].([]interface{})
			for _, v := range rs {
				r, ok := v.(map[string]interface{})
				if !ok {
					continue
				}
				rid, _ := r["route_id"].(string)
				if rid != "" {
					name, _ := r["route_name"].(string)
					out = append(out, workflowTeamRoute{id, rid, name})
				}
			}
		}
	})
	sort.Slice(out, func(i, j int) bool { return out[i].StepID+"/"+out[i].RouteID < out[j].StepID+"/"+out[j].RouteID })
	return out
}
func teamFindRoute(plan map[string]interface{}, a WorkflowRouteAssignment) map[string]interface{} {
	var found map[string]interface{}
	teamWalk(plan, func(step map[string]interface{}) {
		if step["id"] != a.StepID {
			return
		}
		for _, key := range []string{"predefined_routes", "routes"} {
			rs, _ := step[key].([]interface{})
			for _, v := range rs {
				r, ok := v.(map[string]interface{})
				if ok && r["route_id"] == a.RouteID {
					found = r
				}
			}
		}
	})
	return found
}

// Graph routes protect every reachable step; overlapping assignments therefore
// make shared steps immutable until separated. Nested route definitions protect
// the entire child, including its further routes.
func teamRouteSteps(plan map[string]interface{}, route map[string]interface{}) map[string]bool {
	ids := map[string]bool{}
	index := map[string]map[string]interface{}{}
	teamWalk(plan, func(s map[string]interface{}) { id, _ := s["id"].(string); index[id] = s })
	var scan func(interface{})
	scan = func(v interface{}) {
		switch x := v.(type) {
		case map[string]interface{}:
			if id, ok := x["id"].(string); ok {
				if _, isStep := x["type"]; isStep {
					if ids[id] {
						return
					}
					ids[id] = true
				}
			}
			for k, c := range x {
				if k == "next_step_id" || k == "orphan_step_ref" {
					if id, ok := c.(string); ok && index[id] != nil && !ids[id] {
						scan(index[id])
					}
				} else if teamPlanChild(k) {
					scan(c)
				}
			}
		case []interface{}:
			for _, c := range x {
				scan(c)
			}
		}
	}
	scan(route)
	return ids
}
func teamCopy(v map[string]interface{}) map[string]interface{} {
	b, _ := json.Marshal(v)
	r, _ := teamJSON(string(b))
	return r
}
func teamProtectedProjection(plan map[string]interface{}, team *WorkflowTeam, actor string) map[string]interface{} {
	copy := teamCopy(plan)
	for _, a := range team.Routes {
		if a.ContributorID != actor {
			continue
		}
		r := teamFindRoute(copy, a)
		if r == nil {
			continue
		}
		ids := teamRouteSteps(copy, r)
		// Replace only exclusively owned steps. A shared step has every route's protection.
		for _, other := range teamEffectiveAssignments(plan, team) {
			if other.ContributorID == actor {
				continue
			}
			for id := range teamRouteSteps(plan, teamFindRoute(plan, other)) {
				delete(ids, id)
			}
		}
		var mask func(interface{}) interface{}
		mask = func(v interface{}) interface{} {
			switch x := v.(type) {
			case map[string]interface{}:
				if id, _ := x["id"].(string); ids[id] && x["type"] != nil {
					return nil
				}
				for k, c := range x {
					if teamPlanChild(k) {
						x[k] = mask(c)
					}
				}
				return x
			case []interface{}:
				out := []interface{}{}
				for _, c := range x {
					if next := mask(c); next != nil {
						out = append(out, next)
					}
				}
				return out
			default:
				return v
			}
		}
		copy = mask(copy).(map[string]interface{})

		if _, nested := r["sub_agent_step"]; nested {
			r["sub_agent_step"] = nil
		}
	}
	return copy
}
func validateTeamPlanChange(team *WorkflowTeam, actor, before, after string) error {
	old, e := canonicalTeamPlan(before)
	if e != nil {
		return fmt.Errorf("cannot read current plan: %w", e)
	}
	next, e := canonicalTeamPlan(after)
	if e != nil {
		return e
	}
	// Optional step restrictions only narrow authority.
	for _, id := range team.ReadOnlyStepIDs {
		var oldStep, newStep interface{}
		teamWalk(old, func(s map[string]interface{}) {
			if s["id"] == id {
				oldStep = s
			}
		})
		teamWalk(next, func(s map[string]interface{}) {
			if s["id"] == id {
				newStep = s
			}
		})
		if !reflect.DeepEqual(oldStep, newStep) {
			return fmt.Errorf("step %s is read-only under current team policy", id)
		}
	}
	// No actor, including the Leader or administrator, may touch another assignee's route.
	for _, a := range teamEffectiveAssignments(old, team) {
		if a.ContributorID == actor {
			continue
		}
		r := teamFindRoute(old, a)
		n := teamFindRoute(next, a)
		if !reflect.DeepEqual(r, n) {
			return fmt.Errorf("route %s/%s is assigned to another Contributor; explicitly reassign it before editing", a.StepID, a.RouteID)
		}
		ids := teamRouteSteps(old, r)
		oldSteps := map[string]interface{}{}
		newSteps := map[string]interface{}{}
		teamWalk(old, func(s map[string]interface{}) {
			if id, _ := s["id"].(string); ids[id] {
				oldSteps[id] = s
			}
		})
		teamWalk(next, func(s map[string]interface{}) {
			if id, _ := s["id"].(string); ids[id] {
				newSteps[id] = s
			}
		})
		if !reflect.DeepEqual(oldSteps, newSteps) {
			return fmt.Errorf("steps in route %s/%s belong to another Contributor", a.StepID, a.RouteID)
		}
	}
	if actor == team.LeaderID {
		return nil
	}
	if team.member(actor) == nil {
		return fmt.Errorf("not a member of this workflow team")
	}
	if !reflect.DeepEqual(teamProtectedProjection(old, team, actor), teamProtectedProjection(next, team, actor)) {
		return fmt.Errorf("Contributors may edit only steps inside their assigned routes; shared settings require the Leader")
	}
	return nil
}
func teamLock(ctx context.Context, root string) (func(), error) {
	if held, _ := ctx.Value(teamLockHeldKey{}).(string); held == root {
		return func() {}, nil
	}
	return wf.LockWorkspace(ctx, filepath.Join(getWorkspaceDocsAbsPath(), filepath.FromSlash(root)))
}
func validateWorkflowTeam(t *WorkflowTeam, plan map[string]interface{}) error {
	if t == nil || strings.TrimSpace(t.LeaderID) == "" {
		return fmt.Errorf("exactly one Leader is required")
	}
	seen := map[string]bool{t.LeaderID: true}
	for _, m := range t.Contributors {
		if m.UserID == "" || seen[m.UserID] {
			return fmt.Errorf("each person must have one role; Leader cannot also be a Contributor")
		}
		seen[m.UserID] = true
	}
	knownSteps := map[string]bool{}
	teamWalk(plan, func(s map[string]interface{}) { id, _ := s["id"].(string); knownSteps[id] = true })
	for _, id := range t.ReadOnlyStepIDs {
		if !knownSteps[id] {
			return fmt.Errorf("unknown read-only step %s", id)
		}
	}
	routes := map[string]bool{}
	for _, a := range t.Routes {
		k := a.StepID + "/" + a.RouteID
		if routes[k] {
			return fmt.Errorf("a route has exactly one assignee")
		}
		routes[k] = true
		if t.member(a.ContributorID) == nil {
			return fmt.Errorf("route assignee must be a Contributor")
		}
		if teamFindRoute(plan, a) == nil {
			return fmt.Errorf("unknown route %s", k)
		}
	}
	return nil
}
func (api *StreamingAPI) handleWorkflowTeam(w http.ResponseWriter, r *http.Request) {
	root := strings.TrimSpace(r.URL.Query().Get("workspace_path"))
	var body struct {
		WorkspacePath    string        `json:"workspace_path"`
		Team             *WorkflowTeam `json:"team"`
		ExpectedRevision int           `json:"expected_revision"`
	}
	if r.Method == http.MethodPut {
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body) != nil {
			writeUsersError(w, 400, "invalid team policy")
			return
		}
		root = body.WorkspacePath
	}
	normalized, normalizeErr := normalizeReportHumanInputWorkspacePath(root)
	if normalizeErr != nil || !strings.HasPrefix(normalized, "Workflow/") || len(strings.Split(normalized, "/")) != 2 {
		writeUsersError(w, 400, "invalid workspace")
		return
	}
	root = normalized
	release, e := teamLock(r.Context(), root)
	if e != nil {
		writeUsersError(w, 409, e.Error())
		return
	}
	defer release()
	level, m := workflowAccessForWorkspacePath(r.Context(), GetUserFromContext(r.Context()), root)
	if m == nil {
		writeUsersError(w, 404, "workflow not found")
		return
	}
	if level == WorkflowAccessNone {
		writeUsersError(w, 403, "no workflow access")
		return
	}
	raw, exists, e := readFileFromWorkspace(r.Context(), root+"/planning/plan.json")
	if e != nil || !exists {
		writeUsersError(w, 409, "create a valid plan before configuring its team")
		return
	}
	plan, e := teamJSON(raw)
	if e != nil {
		writeUsersError(w, 409, "invalid saved plan")
		return
	}
	if r.Method == http.MethodPut {
		if a := userAccessForClaims(GetUserFromContext(r.Context())); a.Disabled || !a.CanEdit {
			writeUsersError(w, 403, "account does not allow edits")
			return
		}
		actor := teamActor(r.Context())
		if m.Team != nil {
			if !containsID(m.effectiveOwners(), actor) && !userAccessForClaims(GetUserFromContext(r.Context())).Admin {
				writeUsersError(w, 403, "only the Leader can manage the team")
				return
			}
			if body.ExpectedRevision != m.Team.Revision {
				writeUsersError(w, 409, "team policy changed; reload before saving")
				return
			}
		} else if level != WorkflowAccessOwner {
			writeUsersError(w, 403, "only an owner can enable team collaboration")
			return
		} else if body.ExpectedRevision != 0 {
			writeUsersError(w, 409, "team policy changed; reload before enabling")
			return
		}
		if m.Team != nil && body.Team != nil && body.Team.LeaderID != m.Team.LeaderID && body.Team.member(m.Team.LeaderID) == nil {
			writeUsersError(w, 400, "ownership transfer must retain the outgoing Leader as a Contributor with reviewed permissions")
			return
		}
		if e := validateWorkflowTeam(body.Team, plan); e != nil {
			writeUsersError(w, 400, e.Error())
			return
		}
		dir, e := loadUserDirectory()
		if e != nil {
			writeUsersError(w, 500, e.Error())
			return
		}
		for _, id := range append([]string{body.Team.LeaderID}, teamContributorIDs(body.Team)...) {
			if dir.byID(id) == nil || userAccessForClaims(&UserClaims{UserID: id}).Disabled {
				writeUsersError(w, 400, "unknown team member")
				return
			}
		}
		rev := 1
		if m.Team != nil {
			rev = m.Team.Revision + 1
		}
		body.Team.Revision = rev
		if body.Team.Contributors == nil {
			body.Team.Contributors = []WorkflowContributor{}
		}
		if body.Team.Routes == nil {
			body.Team.Routes = []WorkflowRouteAssignment{}
		}
		if !userAccessForClaims(&UserClaims{UserID: body.Team.LeaderID}).CanEdit {
			writeUsersError(w, 400, "Leader must have an editable account")
			return
		}
		kb, everyone := []string{}, false
		if m.Access != nil {
			kb, everyone = m.Access.AllowedKBWriters, m.Access.RunEveryone
		}
		owners := append([]string{}, m.effectiveOwners()...)
		if !containsID(owners, body.Team.LeaderID) {
			owners = append(owners, body.Team.LeaderID)
		}
		readers := append([]string{}, m.effectiveReaders()...)
		m.Team = body.Team
		m.Access = &WorkflowAccess{Owners: owners, Readers: readers, AllowedKBWriters: kb, RunEveryone: everyone}
		if e := WriteWorkflowManifest(context.WithValue(context.WithValue(r.Context(), teamLockHeldKey{}, root), teamManifestPermitKey{}, true), root, m); e != nil {
			writeUsersError(w, 500, e.Error())
			return
		}
		// Invalidate retained native filesystem grants and policy context on reassignment.
		api.invalidateWorkflowTeamSessions(root)
	}
	writeUsersJSON(w, 200, map[string]interface{}{"team": m.Team, "routes": teamRoutes(plan), "my_user_id": teamActor(r.Context()), "plan_revision": teamRevision(raw), "can_manage": userAccessForClaims(GetUserFromContext(r.Context())).CanEdit && (m.Team != nil && (containsID(m.effectiveOwners(), teamActor(r.Context())) || userAccessForClaims(GetUserFromContext(r.Context())).Admin) || m.Team == nil && level == WorkflowAccessOwner)})
}
func teamContributorIDs(t *WorkflowTeam) []string {
	out := []string{}
	for _, m := range t.Contributors {
		out = append(out, m.UserID)
	}
	return out
}

// Plan callbacks keep a per-call read receipt; a whole chat never owns a lock.
type teamPlanReceiptKey struct{}
type teamPlanReceipt struct {
	mu                   sync.Mutex
	reads                map[string]string
	readExists           map[string]bool
	expectedPlanRevision string
	root                 string
	staging              bool
	pending              map[string]string
}

func withTeamPlanReceipt(ctx context.Context) context.Context {
	return context.WithValue(ctx, teamPlanReceiptKey{}, &teamPlanReceipt{reads: map[string]string{}})
}

func (api *StreamingAPI) invalidateWorkflowTeamSessions(root string) {
	if api == nil {
		return
	}
	api.activeSessionsMux.RLock()
	ids := []string{}
	for id, s := range api.activeSessions {
		if strings.Trim(s.WorkspacePath, "/") == strings.Trim(root, "/") && s.AgentMode == "workflow_phase" && s.PhaseID == "workflow-builder" && !isScheduledSessionIdentity(id, s.TriggeredBy) {
			ids = append(ids, id)
		}
	}
	api.activeSessionsMux.RUnlock()
	for _, id := range ids {
		closeCodingCLIAndReleaseTurnMarkers(id, "workflow team permissions changed")
	}
}
func rememberTeamPlanRead(ctx context.Context, path, raw string) {
	if receipt, ok := ctx.Value(teamPlanReceiptKey{}).(*teamPlanReceipt); ok {
		receipt.mu.Lock()
		receipt.reads[path] = raw
		receipt.mu.Unlock()
	}
}
func teamWorkspacePath(input string) (string, string) {
	p := filepath.ToSlash(filepath.Clean(input))
	base := filepath.ToSlash(getWorkspaceDocsAbsPath())
	p = strings.TrimPrefix(p, base+"/")
	parts := strings.Split(p, "/")
	if len(parts) < 2 || !strings.EqualFold(parts[0], "Workflow") {
		return "", ""
	}
	parts[0] = "Workflow"
	// Reject casing aliases conservatively when resolving source ownership; use
	// the physical workflow name for the shared commit lock on local mounts.
	if entries, e := os.ReadDir(filepath.Join(getWorkspaceDocsAbsPath(), "Workflow")); e == nil {
		for _, entry := range entries {
			if entry.IsDir() && strings.EqualFold(entry.Name(), parts[1]) {
				parts[1] = entry.Name()
				break
			}
		}
	}
	return strings.Join(parts[:2], "/"), strings.Join(parts[2:], "/")
}
func teamManagedWrite(ctx context.Context, path, content string) (func(), error) {
	readCurrent := readFileFromWorkspace
	if supplied, ok := ctx.Value(teamCommitReadKey{}).(func(context.Context, string) (string, bool, error)); ok {
		readCurrent = supplied
	}
	root, rel := teamWorkspacePath(path)
	if root == "" {
		return func() {}, nil
	}
	// These permits are created only after the typed writer validates policy.
	// Do not re-enter the migrating manifest reader while saving that manifest,
	// nor while saving the schedule, webhook and function files written with it:
	// a read that repairs the manifest saves it, and re-reading here looped forever.
	if permit, _ := ctx.Value(teamManifestPermitKey{}).(bool); permit && (rel == "workflow.json" || rel == "schedules.json" || rel == "webhooks.json" || rel == "functions.json") {
		return func() {}, nil
	}
	policyCtx := ctx
	if currentPolicy, _ := ctx.Value(workflowSkillsPolicyKey{}).(bool); currentPolicy {
		policyCtx = context.WithValue(ctx, teamPlanReceiptKey{}, nil)
	}
	m, ok, e := ReadWorkflowManifest(policyCtx, root)
	if e != nil {
		return nil, e
	}
	if !ok || m.Team == nil || !teamProtectedSource(rel) {
		return func() {}, nil
	}
	if permit, _ := ctx.Value(teamManifestPermitKey{}).(bool); rel == "workflow.json" && permit {
		return func() {}, nil
	}
	if rel == "workflow.json" {
		return nil, fmt.Errorf("workflow manifest requires a typed settings tool")
	}
	if committing, _ := ctx.Value(teamCommitPermitKey{}).(bool); committing {
		return func() {}, nil
	}
	receipt, ok := ctx.Value(teamPlanReceiptKey{}).(*teamPlanReceipt)
	if !ok {
		return nil, fmt.Errorf("team workflow source changes require a scoped Builder plan tool")
	}
	actor := teamActor(ctx)
	if actor == "" {
		return nil, fmt.Errorf("team mutations require an authenticated actor")
	}
	release := func() {}
	if held, _ := ctx.Value(teamLockHeldKey{}).(string); held != root {
		release, e = teamLock(ctx, root)
		if e != nil {
			return nil, e
		}
	}
	fail := func(e error) (func(), error) { release(); return nil, e }
	// Reload policy inside the same lock as writes and ownership transfer.
	m, ok, e = ReadWorkflowManifest(policyCtx, root)
	if e != nil || !ok || m.Team == nil {
		return fail(fmt.Errorf("workflow team policy changed; retry"))
	}
	if !containsID(m.effectiveOwners(), actor) && m.Team.member(actor) == nil {
		return fail(fmt.Errorf("no current team membership"))
	}
	if access := userAccessForClaims(&UserClaims{UserID: actor}); access.Disabled || !access.CanEdit || IsMultiUserMode() && !access.Known {
		return fail(fmt.Errorf("account is disabled"))
	}
	if strings.HasPrefix(rel, "planning/") && !strings.HasPrefix(rel, "planning/changelog/") && !strings.HasPrefix(rel, "planning/revisions/") {
		planRaw, _, err := readCurrent(context.WithValue(ctx, teamPlanReceiptKey{}, nil), root+"/planning/plan.json")
		if err != nil {
			return fail(err)
		}
		if teamExpectedPlanRevision(receipt) == "" || teamExpectedPlanRevision(receipt) != teamRevision(planRaw) {
			return fail(fmt.Errorf("plan conflict: supply current expected_plan_revision from get_workflow_team, reread and reconcile"))
		}
	}
	if strings.HasPrefix(rel, "planning/changelog/") || strings.HasPrefix(rel, "planning/revisions/") {
		return release, nil
	}
	if rel != "planning/plan.json" && rel != "planning/step_config.json" {
		raw, exists, err := readCurrent(context.WithValue(ctx, teamPlanReceiptKey{}, nil), path)
		if err != nil {
			return fail(err)
		}
		receipt.mu.Lock()
		baseline, read := receipt.reads[path]
		receipt.mu.Unlock()
		if !read || exists && teamRevision(baseline) != teamRevision(raw) {
			return fail(fmt.Errorf("source conflict: reread and reconcile"))
		}
		planRaw, _, err := readCurrent(ctx, root+"/planning/plan.json")
		if err != nil {
			return fail(err)
		}
		plan, err := teamJSON(planRaw)
		if err != nil {
			return fail(err)
		}
		if err := teamSourceAuthority(m.Team, workflowTeamAuthorityActor(m, actor), plan, rel); err != nil {
			return fail(err)
		}
		return release, nil
	}
	current, exists, e := readCurrent(context.WithValue(ctx, teamPlanReceiptKey{}, nil), path)
	if e != nil {
		return fail(e)
	}
	receipt.mu.Lock()
	baseline, read := receipt.reads[path]
	receipt.mu.Unlock()
	if !read || exists && teamRevision(baseline) != teamRevision(current) {
		return fail(fmt.Errorf("plan conflict: saved content changed or was not read; reread and reconcile"))
	}
	if rel == "planning/plan.json" {
		if e := validateTeamPlanChange(m.Team, workflowTeamAuthorityActor(m, actor), current, content); e != nil {
			return fail(e)
		}
	} else {
		planRaw, _, e := readCurrent(ctx, root+"/planning/plan.json")
		if prepared, found := receipt.pending[root+"/planning/plan.json"]; found {
			planRaw = prepared
		}
		if e != nil {
			return fail(e)
		}
		plan, e := teamJSON(planRaw)
		if e != nil {
			return fail(e)
		}
		if e := validateTeamStepConfig(m.Team, workflowTeamAuthorityActor(m, actor), plan, current, content); e != nil {
			return fail(e)
		}
	}
	return release, nil
}
func validateTeamStepConfig(t *WorkflowTeam, actor string, plan map[string]interface{}, before, after string) error {
	old, e := teamJSON(before)
	if e != nil {
		return e
	}
	next, e := teamJSON(after)
	if e != nil {
		return e
	}
	owners := map[string][]string{}
	for _, a := range teamEffectiveAssignments(plan, t) {
		for id := range teamRouteSteps(plan, teamFindRoute(plan, a)) {
			owners[id] = append(owners[id], a.ContributorID)
		}
	}
	project := func(v map[string]interface{}) map[string]interface{} {
		copy := teamCopy(v)
		steps, _ := copy["steps"].([]interface{})
		kept := []interface{}{}
		for _, v := range steps {
			s, ok := v.(map[string]interface{})
			if !ok {
				kept = append(kept, v)
				continue
			}
			id, _ := s["step_id"].(string)
			if id == "" {
				id, _ = s["id"].(string)
			}
			allowed := actor == t.LeaderID && len(owners[id]) == 0
			if len(owners[id]) > 0 {
				allowed = true
				for _, owner := range owners[id] {
					if owner != actor {
						allowed = false
					}
				}
			}
			for _, frozen := range t.ReadOnlyStepIDs {
				if frozen == id {
					allowed = false
				}
			}
			if !allowed {
				kept = append(kept, v)
			}
		}
		copy["steps"] = kept
		return copy
	}
	if !reflect.DeepEqual(project(old), project(next)) {
		return fmt.Errorf("step configuration belongs to another route editor or shared workflow")
	}
	return nil
}

func init() {
	stepworkflow.PlanMutationContext = func(ctx context.Context, args map[string]interface{}) context.Context {
		if _, ok := ctx.Value(teamPlanReceiptKey{}).(*teamPlanReceipt); ok {
			return ctx
		}
		ctx = withTeamPlanReceipt(ctx)
		receipt := ctx.Value(teamPlanReceiptKey{}).(*teamPlanReceipt)
		receipt.expectedPlanRevision, _ = args["expected_plan_revision"].(string)
		return ctx
	}
	workspacepkg.FileReadReceipt = rememberTeamPlanRead
	workspacepkg.FileReadPath = publishedReadPath
	workspacepkg.FileWriteReceipt = teamPlanSaved
	workspacepkg.BeforeFileUpdate = teamManagedWrite
	workspacepkg.AdditionalPathPolicy = func(ctx context.Context, path string, write bool) error {
		if write {
			if reason := publishedWriteRefusal(ctx, path); reason != "" {
				return fmt.Errorf("%s", reason)
			}
		}
		return teamRawPathPolicy(ctx, path, write)
	}
}
func teamProtectedSource(rel string) bool {
	rel = strings.ToLower(rel)
	return rel == "" || rel == "workflow.json" || rel == "soul" || strings.HasPrefix(rel, "soul/") || rel == "planning" || strings.HasPrefix(rel, "planning/") || rel == "code" || strings.HasPrefix(rel, "code/") || rel == "variables" || strings.HasPrefix(rel, "variables/") || rel == "instructions" || strings.HasPrefix(rel, "instructions/") || rel == "learnings" || strings.HasPrefix(rel, "learnings/") || rel == "skills" || strings.HasPrefix(rel, "skills/")
}
func teamRawPathPolicy(ctx context.Context, path string, write bool) error {
	if !write {
		return nil
	}
	if permit, _ := ctx.Value(teamCommitPermitKey{}).(bool); permit {
		return nil
	}
	root, rel := teamWorkspacePath(path)
	if root == "" || !teamProtectedSource(rel) {
		return nil
	}
	if permit, _ := ctx.Value(teamManifestPermitKey{}).(bool); rel == "workflow.json" && permit {
		return nil
	}
	m, ok, e := ReadWorkflowManifest(ctx, root)
	if e != nil {
		return e
	}
	if !ok || m.Team == nil {
		return nil
	}
	if permit, _ := ctx.Value(teamManifestPermitKey{}).(bool); rel == "workflow.json" && permit {
		return nil
	}
	if _, ok := ctx.Value(teamPlanReceiptKey{}).(*teamPlanReceipt); !ok {
		return fmt.Errorf("team source changes require scoped Builder tools; raw writes cannot bypass route assignments")
	}
	return nil
}

type teamManifestPermitKey struct{}

func validateTeamManifestUpdate(ctx context.Context, prior, next *WorkflowManifest) error {
	if prior.Team == nil {
		return nil
	}
	if permit, _ := ctx.Value(teamManifestPermitKey{}).(bool); permit {
		return nil
	}
	actor := teamActor(ctx)
	if actor == "" {
		return fmt.Errorf("team settings changes require an authenticated Leader or schedule manager")
	}
	if !reflect.DeepEqual(prior.Team, next.Team) || !reflect.DeepEqual(prior.Access, next.Access) || prior.CreatedBy != next.CreatedBy {
		return fmt.Errorf("use team settings for membership and ownership transfer")
	}
	if a := userAccessForClaims(&UserClaims{UserID: actor}); a.Disabled || !a.CanEdit || IsMultiUserMode() && !a.Known {
		return fmt.Errorf("account does not allow edits")
	}
	if next.UpdatedAt != prior.UpdatedAt {
		return fmt.Errorf("workflow settings conflict: reload before saving")
	}
	if containsID(prior.effectiveOwners(), actor) {
		return nil
	}
	member := prior.Team.member(actor)
	if member == nil || !member.ManageSchedules || !member.Run {
		return fmt.Errorf("only the Leader manages shared workflow settings; schedule management requires a separate grant")
	}
	old := *prior
	after := *next
	old.Schedules = nil
	after.Schedules = nil
	old.UpdatedAt = ""
	after.UpdatedAt = ""
	if !reflect.DeepEqual(old, after) {
		return fmt.Errorf("schedule managers cannot change shared workflow settings")
	}
	// Only schedules selecting this Contributor's assigned routes can change.
	oldByID := map[string]WorkflowSchedule{}
	for _, s := range prior.Schedules {
		oldByID[s.ID] = s
	}
	newByID := map[string]WorkflowSchedule{}
	for _, s := range next.Schedules {
		newByID[s.ID] = s
		if previous, ok := oldByID[s.ID]; ok && reflect.DeepEqual(previous, s) {
			continue
		}
		if e := teamScheduleAuthority(prior.Team, actor, s); e != nil {
			return e
		}
	}
	for id, s := range oldByID {
		if _, ok := newByID[id]; !ok {
			if e := teamScheduleAuthority(prior.Team, actor, s); e != nil {
				return e
			}
		}
	}
	return nil
}
func teamScheduleAuthority(t *WorkflowTeam, actor string, s WorkflowSchedule) error {
	if actor == t.LeaderID {
		return nil
	}
	member := t.member(actor)
	if member == nil || !member.ManageSchedules || !member.Run {
		return fmt.Errorf("schedule management and route run permissions are required")
	}
	if len(s.RouteSelections) == 0 || len(s.Messages) > 0 {
		return fmt.Errorf("Contributors may manage only schedules with explicit assigned routes and no arbitrary messages")
	}
	for step, route := range s.RouteSelections {
		allowed := false
		for _, a := range t.Routes {
			if a.StepID == step && a.RouteID == route && a.ContributorID == actor {
				allowed = true
			}
		}
		if !allowed {
			return fmt.Errorf("schedule selects a route not assigned to you")
		}
	}
	return nil
}

func teamSourceOwners(t *WorkflowTeam, plan map[string]interface{}, rel string) []string {
	owners := []string{}
	for _, a := range teamEffectiveAssignments(plan, t) {
		route := teamFindRoute(plan, a)
		ids := teamRouteSteps(plan, route)
		used := false
		for id := range ids {
			if strings.HasPrefix(strings.ToLower(rel), "code/"+strings.ToLower(id)+"/") || strings.HasPrefix(strings.ToLower(rel), "learnings/"+strings.ToLower(id)+"/") {
				used = true
			}
		}
		teamWalk(plan, func(s map[string]interface{}) {
			if id, _ := s["id"].(string); ids[id] {
				used = used || teamSourceReferenced(s, rel)
			}
		})
		if used {
			owners = append(owners, a.ContributorID)
		}
	}
	return owners
}
func teamSourceAuthority(t *WorkflowTeam, actor string, plan map[string]interface{}, rel string) error {
	for _, id := range t.ReadOnlyStepIDs {
		if strings.HasPrefix(strings.ToLower(rel), "code/"+strings.ToLower(id)+"/") || strings.HasPrefix(strings.ToLower(rel), "learnings/"+strings.ToLower(id)+"/") {
			return fmt.Errorf("source belongs to a read-only step")
		}
		used := false
		teamWalk(plan, func(step map[string]interface{}) {
			if step["id"] == id && teamSourceReferenced(step, rel) {
				used = true
			}
		})
		if used {
			return fmt.Errorf("source is referenced by a read-only step")
		}
	}
	owners := teamSourceOwners(t, plan, rel)
	if len(owners) == 0 {
		if actor != t.LeaderID {
			return fmt.Errorf("shared source belongs to the Leader")
		}
		return nil
	}
	for _, owner := range owners {
		if owner != actor {
			return fmt.Errorf("source is used by another Contributor's route; reassign or separate it before editing")
		}
	}
	return nil
}

func teamSourceReferenced(value interface{}, rel string) bool {
	switch v := value.(type) {
	case string:
		return strings.EqualFold(strings.TrimPrefix(v, "./"), rel)
	case map[string]interface{}:
		for _, child := range v {
			if teamSourceReferenced(child, rel) {
				return true
			}
		}
	case []interface{}:
		for _, child := range v {
			if teamSourceReferenced(child, rel) {
				return true
			}
		}
	}
	return false
}
func (api *StreamingAPI) registerWorkflowTeamTools(reg definitionToolRegistrar, root, user string, readOnly bool) error {
	if root == "" {
		return nil
	}
	schema := map[string]interface{}{"type": "object", "properties": map[string]interface{}{}, "additionalProperties": false}
	if e := reg.RegisterCustomTool("get_workflow_team", "Read the current Leader, Contributors, assigned route editors and plan revision. Assignment is exclusive: even the Leader cannot edit another Contributor's route. Multiple Builder chats can work concurrently.", schema, func(ctx context.Context, _ map[string]interface{}) (string, error) {
		_, m := workflowAccessForWorkspacePath(ctx, &UserClaims{UserID: user}, root)
		if m == nil {
			return "", fmt.Errorf("workflow not found")
		}
		if workflowAccessForManifest(&UserClaims{UserID: user}, m) == WorkflowAccessNone {
			return "", fmt.Errorf("no team access")
		}
		raw, _, e := readFileFromWorkspace(ctx, root+"/planning/plan.json")
		if e != nil {
			return "", e
		}
		b, e := json.Marshal(map[string]interface{}{"team": m.Team, "routes": teamRoutesMustParse(raw), "my_user_id": user, "plan_revision": teamRevision(raw)})
		return string(b), e
	}, "workflow"); e != nil {
		return e
	}
	if err := reg.RegisterCustomTool("read_team_source", "Read a team workflow source and its expected_revision for edit_team_source.", map[string]interface{}{"type": "object", "properties": map[string]interface{}{"path": map[string]interface{}{"type": "string"}}, "required": []string{"path"}, "additionalProperties": false}, func(ctx context.Context, args map[string]interface{}) (string, error) {
		if level, _ := workflowAccessForWorkspacePath(ctx, &UserClaims{UserID: user}, root); level == WorkflowAccessNone {
			return "", fmt.Errorf("no workflow access")
		}
		rel := filepath.ToSlash(filepath.Clean(stringToolArg(args, "path")))
		if strings.HasPrefix(rel, "../") || filepath.IsAbs(rel) || !(strings.HasPrefix(rel, "code/") || strings.HasPrefix(rel, "learnings/") || strings.HasPrefix(rel, "instructions/") || strings.HasPrefix(rel, "skills/") || strings.HasPrefix(rel, "variables/") || rel == "soul/soul.md") {
			return "", fmt.Errorf("unsupported source path")
		}
		raw, exists, err := readFileFromWorkspace(ctx, root+"/"+rel)
		if err != nil {
			return "", err
		}
		rev := "missing"
		if exists {
			rev = teamRevision(raw)
		}
		b, err := json.Marshal(map[string]interface{}{"content": raw, "expected_revision": rev})
		return string(b), err
	}, "workflow"); err != nil {
		return err
	}
	if readOnly {
		return nil
	}
	props := map[string]interface{}{"path": map[string]interface{}{"type": "string"}, "content": map[string]interface{}{"type": "string"}, "expected_revision": map[string]interface{}{"type": "string"}}
	return reg.RegisterCustomTool("edit_team_source", "Edit a team workflow source file after reading it. expected_revision is the SHA256 of the exact saved UTF-8 content (missing for a new file). Only the route assignee edits referenced source; the Leader edits shared source and soul/soul.md. Plans and membership use typed tools. Changes fail if the revision or assignment changed.", map[string]interface{}{"type": "object", "properties": props, "required": []string{"path", "content", "expected_revision"}, "additionalProperties": false}, func(ctx context.Context, args map[string]interface{}) (string, error) {
		rel := filepath.ToSlash(filepath.Clean(stringToolArg(args, "path")))
		if strings.HasPrefix(rel, "../") || filepath.IsAbs(rel) || !(strings.HasPrefix(rel, "code/") || strings.HasPrefix(rel, "learnings/") || strings.HasPrefix(rel, "instructions/") || strings.HasPrefix(rel, "skills/") || strings.HasPrefix(rel, "variables/") || rel == "soul/soul.md") {
			return "", fmt.Errorf("only code/, instructions/ or soul/soul.md sources may be edited")
		}
		if level, _ := workflowAccessForWorkspacePath(ctx, &UserClaims{UserID: user}, root); level != WorkflowAccessOwner && level != WorkflowAccessWrite {
			return "", fmt.Errorf("source edit requires write access")
		}
		if a := userAccessForClaims(&UserClaims{UserID: user}); a.Disabled || !a.CanEdit {
			return "", fmt.Errorf("account does not allow edits")
		}
		ctx = teamUserContext(withTeamPlanReceipt(ctx), user)
		unlock, lockErr := teamLock(ctx, root)
		if lockErr != nil {
			return "", lockErr
		}
		defer unlock()
		ctx = context.WithValue(ctx, teamLockHeldKey{}, root)
		raw, exists, e := readFileFromWorkspace(ctx, root+"/"+rel)
		if e != nil {
			return "", e
		}
		expected := "missing"
		if exists {
			expected = teamRevision(raw)
		}
		if expected != stringToolArg(args, "expected_revision") {
			return "", fmt.Errorf("source conflict: reread and reconcile")
		}
		rememberTeamPlanRead(ctx, root+"/"+rel, raw)
		if e := writeFileToWorkspace(ctx, root+"/"+rel, stringToolArg(args, "content")); e != nil {
			return "", e
		}
		// The source replacement already passed current ownership and revision
		// checks. Attribute its shared history even if membership changes next.
		historyCtx := context.WithValue(ctx, teamCommitPermitKey{}, true)
		historyCtx = stepworkflow.WithWorkflowSettingsOrigin(historyCtx, user, logUsernameForUserID(user))
		stepworkflow.LogCanonicalArtifactChange(historyCtx, root, "edit_team_source", "Updated workflow source through a revision-checked team tool.", nil, workflowManifestChangelogReader, writeFileToWorkspace, createServerLogger(), rel, raw, stringToolArg(args, "content"))
		return `{"status":"saved"}`, nil
	}, "workflow")
}

func stampTeamScheduleChanges(ctx context.Context, prior, next *WorkflowManifest) {
	if next.Team == nil {
		return
	}
	actor := teamActor(ctx)
	old := map[string]WorkflowSchedule{}
	for _, s := range prior.Schedules {
		old[s.ID] = s
	}
	for i := range next.Schedules {
		if previous, ok := old[next.Schedules[i].ID]; !ok || !reflect.DeepEqual(previous, next.Schedules[i]) {
			next.Schedules[i].TeamAuthorizedBy = actor
			next.Schedules[i].TeamPolicyRevision = next.Team.Revision
		}
	}
}

// Explicit reauthorization preserves the reviewed schedule itself. It uses
// the same current grants and manifest writer as ordinary schedule edits.
func workflowTeamScheduleRevision(s WorkflowSchedule) (string, error) {
	raw, err := json.Marshal(s)
	return teamRevision(string(raw)), err
}

func reauthorizeWorkflowTeamSchedule(ctx context.Context, root string, m *WorkflowManifest, id string, revision int, expectedSchedule string) error {
	if held, _ := ctx.Value(teamLockHeldKey{}).(string); held != root {
		return fmt.Errorf("team authorization requires the workspace lock")
	}
	if m.Team == nil || m.Team.Revision != revision {
		return fmt.Errorf("team policy changed; read the current revision before authorizing")
	}
	if access := userAccessForClaims(GetUserFromContext(ctx)); access.Disabled || !access.CanEdit {
		return fmt.Errorf("account does not allow edits")
	}
	actor := teamActor(ctx)
	for i := range m.Schedules {
		if m.Schedules[i].ID != id {
			continue
		}
		current, err := workflowTeamScheduleRevision(m.Schedules[i])
		if err != nil {
			return err
		}
		if current != expectedSchedule {
			return fmt.Errorf("schedule changed; reread before authorizing")
		}
		if err := teamScheduleAuthority(m.Team, workflowTeamAuthorityActor(m, actor), m.Schedules[i]); err != nil {
			return err
		}
		m.Schedules[i].TeamAuthorizedBy = actor
		m.Schedules[i].TeamPolicyRevision = m.Team.Revision
		return WriteWorkflowManifest(ctx, root, m)
	}
	return fmt.Errorf("schedule not found in this workflow")
}
func validateTeamScheduleFire(ctx context.Context, root string, s WorkflowSchedule) error {
	m, ok, e := ReadWorkflowManifest(ctx, root)
	if e != nil {
		return e
	}
	if !ok || m.Team == nil {
		return nil
	}
	if a := userAccessForClaims(&UserClaims{UserID: s.TeamAuthorizedBy}); a.Disabled || !a.CanEdit || IsMultiUserMode() && !a.Known {
		return fmt.Errorf("schedule authorizer is disabled or read-only")
	}
	current := false
	for _, saved := range m.Schedules {
		if saved.ID == s.ID && reflect.DeepEqual(saved, s) {
			current = true
		}
	}
	if !current {
		return fmt.Errorf("schedule changed or was removed; retry from current manifest")
	}
	if s.TeamPolicyRevision != m.Team.Revision || s.TeamAuthorizedBy == "" {
		return fmt.Errorf("team schedule authorization changed; the Leader must review and save this schedule")
	}
	return teamScheduleAuthority(m.Team, workflowTeamAuthorityActor(m, s.TeamAuthorizedBy), s)
}

func teamPlanSaved(ctx context.Context, path, content string) {
	if receipt, ok := ctx.Value(teamPlanReceiptKey{}).(*teamPlanReceipt); ok {
		receipt.mu.Lock()
		receipt.reads[path] = content
		if strings.HasSuffix(path, "/planning/plan.json") {
			receipt.expectedPlanRevision = teamRevision(content)
		}
		receipt.mu.Unlock()
	}
}

func teamExpectedPlanRevision(r *teamPlanReceipt) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.expectedPlanRevision
}
func teamRoutesMustParse(raw string) []workflowTeamRoute {
	p, e := teamJSON(raw)
	if e != nil {
		return nil
	}
	return teamRoutes(p)
}
func teamUserContext(ctx context.Context, user string) context.Context {
	ctx = context.WithValue(ctx, common.UserIDKey, user)
	claims := GetUserFromContext(ctx)
	if claims == nil || claims.UserID != user {
		ctx = context.WithValue(ctx, UserContextKey, &UserClaims{UserID: user})
	}
	return ctx
}

type teamLockHeldKey struct{}
type teamCommitPermitKey struct{}

func teamExternalSourceAuthority(ctx context.Context, root, rel string) error {
	m, ok, e := ReadWorkflowManifest(ctx, root)
	if e != nil {
		return e
	}
	if !ok || m.Team == nil {
		return nil
	}
	actor := teamActor(ctx)
	if a := userAccessForClaims(&UserClaims{UserID: actor}); a.Disabled || !a.CanEdit || IsMultiUserMode() && !a.Known {
		return fmt.Errorf("account does not allow edits")
	}
	if !containsID(m.effectiveOwners(), actor) && m.Team.member(actor) == nil {
		return fmt.Errorf("no current team membership")
	}
	raw, _, e := readFileFromWorkspace(ctx, root+"/planning/plan.json")
	if e != nil {
		return e
	}
	plan, e := teamJSON(raw)
	if e != nil {
		return e
	}
	return teamSourceAuthority(m.Team, workflowTeamAuthorityActor(m, actor), plan, rel)
}

func teamEffectiveAssignments(plan map[string]interface{}, t *WorkflowTeam) []WorkflowRouteAssignment {
	out := []WorkflowRouteAssignment{}
	for _, route := range teamRoutes(plan) {
		actor := t.LeaderID
		explicit := false
		for _, a := range t.Routes {
			if a.StepID == route.StepID && a.RouteID == route.RouteID {
				actor = a.ContributorID
				explicit = true
				break
			}
		}
		if !explicit {
			for _, a := range t.Routes {
				if teamRouteSteps(plan, teamFindRoute(plan, a))[route.StepID] {
					actor = a.ContributorID
					break
				}
			}
		}
		out = append(out, WorkflowRouteAssignment{StepID: route.StepID, RouteID: route.RouteID, ContributorID: actor})
	}
	return out
}

func workflowTeamInstructions(ctx context.Context, root, user string) string {
	m, found, e := ReadWorkflowManifest(ctx, root)
	if e != nil || !found || m.Team == nil {
		return ""
	}
	role := "Contributor"
	if containsID(m.effectiveOwners(), user) {
		role = "Leader"
	}
	return "## Goal team\n\nYou act for " + role + " " + user + ". Call get_workflow_team for current assignments and plan_revision before proposing a change. Use that revision as expected_plan_revision with native plan/config tools; conflicts require rereading and reconciling. Only an assigned Contributor edits its route's steps; the Leader must explicitly reassign or take back the route first. Source edits use read_team_source then edit_team_source with expected_revision. Raw source writes, moves and deletes cannot bypass this. Only the Leader changes the overall goal and shared settings. Execution, schedule management, Backup, Publish, Notify and named decision approvals are separate grants. Share relevant results with AgentWorks through explicit messages; Project owners share registered named Builder tabs and control only their own; earlier private history stays private. Separate Builder chats may run concurrently, but never overwrite a conflicting saved edit."
}

// Compare executable plan shapes, including the same default/canonical fields
// that native tools serialize. Ordinary DTO normalization must not look like a
// Contributor editing every other route. Root metadata remains protected.
func canonicalTeamPlan(raw string) (map[string]interface{}, error) {
	original, e := teamJSON(raw)
	if e != nil {
		return nil, e
	}
	var plan stepworkflow.PlanningResponse
	if e = json.Unmarshal([]byte(raw), &plan); e != nil {
		return nil, e
	}
	normalized, e := json.Marshal(plan)
	if e != nil {
		return nil, e
	}
	canonical, e := teamJSON(string(normalized))
	if e != nil {
		return nil, e
	}
	for _, key := range []string{"steps", "orphan_steps"} {
		original[key] = canonical[key]
	}
	return original, nil
}

// Co-owners have the same project authority as its execution leader. Keep the
// real actor for audit, cost and chat ownership; only Team permission checks
// use this projection. Contributor assignments retain their existing rules.
func workflowTeamAuthorityActor(m *WorkflowManifest, actor string) string {
	if m != nil && m.Team != nil && containsID(m.effectiveOwners(), actor) {
		return m.Team.LeaderID
	}
	return actor
}
