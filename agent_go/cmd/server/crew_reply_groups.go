package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/filesnapshots"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/commands"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

const replyGroupMaxPRLookups = 5
const replyGroupMaxFailingChecks = 10

// replyFileGroups groups a reply's changes by Git checkout and, with
// pullRequests, adds pull request status from the Crew owner's connected
// GitHub account and each checkout's next Git step. The server's own GitHub
// login is never used.
func replyFileGroups(ctx context.Context, roots crewroot.Roots, prefix string, comparison filesnapshots.Comparison, allow func(string) bool, pullRequests bool) []filesnapshots.Group {
	groups := filesnapshots.GroupChanges(ctx, roots.Files, comparison.Changes, comparison.NewRepos, allow)
	if pullRequests {
		token, connected := "", false
		if rec := readGitHubRecord(roots.Owner); rec != nil {
			token = githubTokenValue(rec)
			connected = token != ""
		}
		attachPullRequests(ctx, groups, token, connected)
	}
	for i := range groups {
		if pullRequests && groups[i].Kind == "repo" {
			groups[i].NextStep = filesnapshots.NextStep(groups[i])
		}
		if groups[i].Root != "" {
			groups[i].Root = prefix + groups[i].Root
		}
		for j := range groups[i].Files {
			groups[i].Files[j].Path = prefix + groups[i].Files[j].Path
		}
	}
	return groups
}

// summaryGroups returns the groups the reply event carries (computed when the
// reply's files were captured), which the card lists.
func (h chatFileReview) summaryGroups(id string) ([]filesnapshots.Group, bool) {
	var data struct {
		Summary *struct {
			Groups []filesnapshots.Group `json:"groups"`
		} `json:"summary"`
	}
	raw, ok := h.replyData[id]
	if !ok || json.Unmarshal(raw, &data) != nil || data.Summary == nil {
		return nil, false
	}
	return data.Summary.Groups, true
}

// replyCard is what a reply card shows for this viewer: its groups and
// action list. The Git actions, card commands and Undo follow the Crew
// screens' Builder check (the same one as /api/workspace-access); everyone
// else gets Review changes only.
func (api *StreamingAPI) replyCard(r *http.Request, root string, groups []filesnapshots.Group) map[string]any {
	canEdit := false
	if claims := GetUserFromContext(r.Context()); claims != nil && claims.AccessToken == nil && claims.Scope == "" {
		if target, err := api.dashboardTarget(r.Context(), claims, root); err == nil {
			canEdit = target.Edit
		}
	}
	var cardCommands []commands.Command
	if canEdit {
		// Same path and access rule as listing the Crew's commands.
		if path, ok := commandPathForRequest(discardResponse{}, r, false); ok {
			if list, err := commands.DiscoverCommandsAt(getWorkspaceAPIURL(), path); err == nil {
				cardCommands = list
			}
		}
	}
	return map[string]any{"groups": groups, "actions": filesnapshots.CardActions(groups, canEdit, cardCommands), "can_edit": canEdit}
}

// discardResponse drops what a shared request check writes when its result is
// only used inside another response.
type discardResponse struct{}

func (discardResponse) Header() http.Header         { return http.Header{} }
func (discardResponse) Write(b []byte) (int, error) { return len(b), nil }
func (discardResponse) WriteHeader(int)             {}

// attachPullRequests resolves each GitHub-backed branch's pull request.
func attachPullRequests(ctx context.Context, groups []filesnapshots.Group, token string, connected bool) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	lookups := 0
	for i := range groups {
		g := &groups[i]
		if g.Kind != "repo" || g.GitHubRepo == "" || g.Branch == "" || strings.HasPrefix(g.Branch, "detached@") || g.Branch == "main" || g.Branch == "master" {
			continue
		}
		if !connected {
			g.PR = &filesnapshots.PullRequest{State: "connect_github"}
			continue
		}
		if lookups++; lookups > replyGroupMaxPRLookups {
			break
		}
		g.PR = githubPullRequestStatus(ctx, token, g.GitHubRepo, g.Branch)
	}
}

func githubGET(ctx context.Context, token, path string, out any) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, githubAPIURL+path, nil)
	if err != nil {
		return false
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := githubHTTPClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK && json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(out) == nil
}

// githubPullRequestStatus returns nil when the branch has no pull request,
// and state "unavailable" when GitHub could not answer.
func githubPullRequestStatus(ctx context.Context, token, repo, branch string) *filesnapshots.PullRequest {
	owner := strings.SplitN(repo, "/", 2)[0]
	var prs []struct {
		Number   int     `json:"number"`
		URL      string  `json:"html_url"`
		State    string  `json:"state"`
		MergedAt *string `json:"merged_at"`
		Head     struct {
			SHA string `json:"sha"`
		} `json:"head"`
	}
	q := url.Values{"head": {owner + ":" + branch}, "state": {"all"}, "sort": {"updated"}, "direction": {"desc"}, "per_page": {"1"}}
	if !githubGET(ctx, token, "/repos/"+repo+"/pulls?"+q.Encode(), &prs) {
		return &filesnapshots.PullRequest{State: "unavailable"}
	}
	if len(prs) == 0 {
		return nil
	}
	p := prs[0]
	pr := &filesnapshots.PullRequest{Number: p.Number, URL: p.URL, State: p.State, Checks: "none", Review: "none"}
	if p.MergedAt != nil {
		pr.State = "merged"
	}
	var runs struct {
		CheckRuns []struct {
			Name       string `json:"name"`
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
		} `json:"check_runs"`
	}
	var combined struct {
		State      string `json:"state"`
		TotalCount int    `json:"total_count"`
		Statuses   []struct {
			Context string `json:"context"`
			State   string `json:"state"`
		} `json:"statuses"`
	}
	failed := func(name string) {
		if len(pr.FailingChecks) < replyGroupMaxFailingChecks {
			pr.FailingChecks = append(pr.FailingChecks, name)
		}
	}
	failing, pending, seen := false, false, false
	if githubGET(ctx, token, "/repos/"+repo+"/commits/"+p.Head.SHA+"/check-runs?per_page=100", &runs) {
		for _, run := range runs.CheckRuns {
			seen = true
			switch {
			case run.Status != "completed":
				pending = true
			case run.Conclusion == "failure" || run.Conclusion == "timed_out" || run.Conclusion == "cancelled" || run.Conclusion == "action_required" || run.Conclusion == "startup_failure":
				failing = true
				failed(run.Name)
			}
		}
	}
	if githubGET(ctx, token, "/repos/"+repo+"/commits/"+p.Head.SHA+"/status", &combined) && combined.TotalCount > 0 {
		seen = true
		switch combined.State {
		case "failure", "error":
			failing = true
		case "pending":
			pending = true
		}
		for _, status := range combined.Statuses {
			if status.State == "failure" || status.State == "error" {
				failed(status.Context)
			}
		}
	}
	switch {
	case failing:
		pr.Checks = "failing"
	case pending:
		pr.Checks = "pending"
	case seen:
		pr.Checks = "passing"
	}
	var reviews []struct {
		State string `json:"state"`
		User  struct {
			Login string `json:"login"`
		} `json:"user"`
	}
	if githubGET(ctx, token, "/repos/"+repo+"/pulls/"+strconv.Itoa(p.Number)+"/reviews?per_page=100", &reviews) {
		latest := map[string]string{}
		for _, r := range reviews { // oldest first: the last decision per person wins
			if r.State == "APPROVED" || r.State == "CHANGES_REQUESTED" || r.State == "DISMISSED" {
				latest[r.User.Login] = r.State
			}
		}
		for _, state := range latest {
			if state == "CHANGES_REQUESTED" {
				pr.Review = "changes_requested"
				break
			}
			if state == "APPROVED" {
				pr.Review = "approved"
			}
		}
	}
	// Only the single-PR read says whether it can merge; "dirty" means conflicts.
	var detail struct {
		MergeableState string `json:"mergeable_state"`
	}
	if pr.State == "open" && githubGET(ctx, token, "/repos/"+repo+"/pulls/"+strconv.Itoa(p.Number), &detail) {
		pr.Conflicts = detail.MergeableState == "dirty"
	}
	return pr
}
