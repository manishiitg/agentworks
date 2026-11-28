package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/filesnapshots"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/commands"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

const replyGroupMaxPRLookups = 5
const replyGroupMaxFailingChecks = 10

const (
	replyGroupCacheTTL        = 30 * time.Second
	replyGroupCacheStaleTTL   = 5 * time.Minute
	replyGroupCacheMaxEntries = 256
)

var replyGroupChangesFn = filesnapshots.GroupChanges

var replyGroupCache = struct {
	sync.Mutex
	entries    map[string]replyGroupCacheEntry
	refreshing map[string]bool
	group      singleflight.Group
}{
	entries:    make(map[string]replyGroupCacheEntry),
	refreshing: make(map[string]bool),
}

type replyGroupCacheEntry struct {
	groups     []filesnapshots.Group
	expires    time.Time
	staleUntil time.Time
}

func cloneFileSnapshotGroups(groups []filesnapshots.Group) []filesnapshots.Group {
	if groups == nil {
		return nil
	}
	out := make([]filesnapshots.Group, len(groups))
	for i, g := range groups {
		if g.Files != nil {
			g.Files = append(make([]filesnapshots.Change, 0, len(g.Files)), g.Files...)
		}
		if g.Uncommitted != nil {
			g.Uncommitted = append(make([]filesnapshots.Change, 0, len(g.Uncommitted)), g.Uncommitted...)
		}
		if g.PR != nil {
			prCopy := *g.PR
			if prCopy.FailingChecks != nil {
				prCopy.FailingChecks = append([]string(nil), prCopy.FailingChecks...)
			}
			g.PR = &prCopy
		}
		out[i] = g
	}
	return out
}

func replyGroupCacheKey(person, filesRoot string, comparison filesnapshots.Comparison) string {
	h := sha256.New()
	writePart := func(s string) {
		_, _ = h.Write([]byte(s))
		_, _ = h.Write([]byte{0})
	}
	writePart(person)
	writePart(filesRoot)
	writePart(comparison.Record.ID)
	writePart(strconv.Itoa(len(comparison.NewRepos)))
	for _, repo := range comparison.NewRepos {
		writePart(repo)
	}
	writePart(strconv.Itoa(len(comparison.Changes)))
	for _, c := range comparison.Changes {
		writePart(c.Path)
		writePart(c.Status)
		if c.Checkout != nil {
			writePart(c.Checkout.ID)
			writePart(c.Checkout.Branch)
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

func replyGroupsAllowed(groups []filesnapshots.Group, allow func(string) bool) bool {
	if allow == nil {
		return true
	}
	for _, g := range groups {
		for _, f := range g.Files {
			if !allow(f.Path) {
				return false
			}
		}
	}
	return true
}

func storeReplyGroupCacheEntry(key string, groups []filesnapshots.Group, now time.Time) {
	replyGroupCache.Lock()
	for k, cached := range replyGroupCache.entries {
		if now.After(cached.staleUntil) {
			delete(replyGroupCache.entries, k)
		}
	}
	if len(replyGroupCache.entries) >= replyGroupCacheMaxEntries {
		for k := range replyGroupCache.entries {
			delete(replyGroupCache.entries, k)
			break
		}
	}
	replyGroupCache.entries[key] = replyGroupCacheEntry{
		groups:     cloneFileSnapshotGroups(groups),
		expires:    now.Add(replyGroupCacheTTL),
		staleUntil: now.Add(replyGroupCacheStaleTTL),
	}
	replyGroupCache.Unlock()
}

func cachedReplyGroupChanges(ctx context.Context, roots crewroot.Roots, comparison filesnapshots.Comparison, allow func(string) bool) []filesnapshots.Group {
	if comparison.Record.ID == "" {
		return replyGroupChangesFn(ctx, roots.Files, comparison.Changes, comparison.NewRepos, allow)
	}
	key := replyGroupCacheKey(GetUserIDFromContext(ctx), roots.Files, comparison)
	now := time.Now()
	replyGroupCache.Lock()
	if entry, ok := replyGroupCache.entries[key]; ok && now.Before(entry.staleUntil) {
		cached := cloneFileSnapshotGroups(entry.groups)
		needsRefresh := !now.Before(entry.expires) && !replyGroupCache.refreshing[key]
		if needsRefresh {
			replyGroupCache.refreshing[key] = true
		}
		replyGroupCache.Unlock()
		if !replyGroupsAllowed(cached, allow) {
			if needsRefresh {
				replyGroupCache.Lock()
				delete(replyGroupCache.refreshing, key)
				replyGroupCache.Unlock()
			}
			return replyGroupChangesFn(ctx, roots.Files, comparison.Changes, comparison.NewRepos, allow)
		}
		if needsRefresh {
			filesRoot := roots.Files
			changesCopy := append([]filesnapshots.Change(nil), comparison.Changes...)
			newReposCopy := append([]string(nil), comparison.NewRepos...)
			go func() {
				defer func() {
					replyGroupCache.Lock()
					delete(replyGroupCache.refreshing, key)
					replyGroupCache.Unlock()
				}()
				bgCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				groups := replyGroupChangesFn(bgCtx, filesRoot, changesCopy, newReposCopy, allow)
				if bgCtx.Err() == nil {
					storeReplyGroupCacheEntry(key, groups, time.Now())
				}
			}()
		}
		return cached
	}
	replyGroupCache.Unlock()

	value, _, _ := replyGroupCache.group.Do(key, func() (any, error) {
		now := time.Now()
		replyGroupCache.Lock()
		if entry, ok := replyGroupCache.entries[key]; ok && now.Before(entry.expires) {
			cached := cloneFileSnapshotGroups(entry.groups)
			replyGroupCache.Unlock()
			return cached, nil
		}
		replyGroupCache.Unlock()

		groups := replyGroupChangesFn(ctx, roots.Files, comparison.Changes, comparison.NewRepos, allow)
		if ctx.Err() == nil {
			storeReplyGroupCacheEntry(key, groups, time.Now())
		}
		return groups, nil
	})
	groups := cloneFileSnapshotGroups(value.([]filesnapshots.Group))
	if !replyGroupsAllowed(groups, allow) {
		return replyGroupChangesFn(ctx, roots.Files, comparison.Changes, comparison.NewRepos, allow)
	}
	return groups
}

// replyFileGroups groups a reply's changes by Git checkout and, with
// pullRequests, adds pull request status from the Crew owner's connected
// GitHub account and each checkout's next Git step. The server's own GitHub
// login is never used. chatStarts (a chat summary only) adds each checkout's
// uncommitted files and the commits made since the chat's starting commit.
func replyFileGroups(ctx context.Context, roots crewroot.Roots, prefix string, comparison filesnapshots.Comparison, allow func(string) bool, pullRequests bool, chatStarts map[string]string) []filesnapshots.Group {
	groups := cachedReplyGroupChanges(ctx, roots, comparison, allow)
	if pullRequests {
		token, connected := "", false
		if rec, _ := projectGitHubConnection(ctx, roots.Owner, roots.Virtual); rec != nil {
			token = githubTokenValue(rec)
			connected = token != ""
		}
		attachPullRequests(ctx, groups, token, connected)
	}
	for i := range groups {
		if pullRequests && groups[i].Kind == "repo" {
			groups[i].NextStep = filesnapshots.NextStep(groups[i])
		}
		if chatStarts != nil {
			filesnapshots.AddChatGitState(ctx, roots.Files, &groups[i], chatStarts[groups[i].Root], allow)
			for j := range groups[i].Uncommitted {
				groups[i].Uncommitted[j].Path = prefix + groups[i].Uncommitted[j].Path
			}
		}
		if groups[i].Root != "" {
			groups[i].Root = prefix + groups[i].Root
		}
		for j := range groups[i].Files {
			groups[i].Files[j] = groups[i].Files[j].WithPrefix(prefix)
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
func (api *StreamingAPI) replyCardCanEdit(r *http.Request, root string) bool {
	canEdit := false
	if claims := GetUserFromContext(r.Context()); claims != nil && claims.AccessToken == nil && claims.Scope == "" {
		if target, err := api.dashboardTarget(r.Context(), claims, root); err == nil {
			canEdit = target.Edit
		}
	}
	return canEdit
}

func (api *StreamingAPI) replyCard(r *http.Request, root string, groups []filesnapshots.Group) map[string]any {
	canEdit := api.replyCardCanEdit(r, root)
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
// Up to replyGroupMaxPRLookups eligible repos are resolved concurrently.
func attachPullRequests(ctx context.Context, groups []filesnapshots.Group, token string, connected bool) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	var targets []int
	for i := range groups {
		g := &groups[i]
		if g.Kind != "repo" || g.GitHubRepo == "" || g.Branch == "" || strings.HasPrefix(g.Branch, "detached@") || g.Branch == "main" || g.Branch == "master" {
			continue
		}
		if !connected {
			g.PR = &filesnapshots.PullRequest{State: "connect_github"}
			continue
		}
		if len(targets) >= replyGroupMaxPRLookups {
			break
		}
		targets = append(targets, i)
	}
	if len(targets) == 1 {
		g := &groups[targets[0]]
		g.PR = cachedGitHubPullRequestStatus(ctx, token, g.GitHubRepo, g.Branch)
		return
	}
	var wg sync.WaitGroup
	for _, idx := range targets {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			g := &groups[i]
			g.PR = cachedGitHubPullRequestStatus(ctx, token, g.GitHubRepo, g.Branch)
		}(idx)
	}
	wg.Wait()
}

const (
	replyPRFreshTTL        = 45 * time.Second
	replyPRSettledFreshTTL = 5 * time.Minute
	replyPRStaleTTL        = 10 * time.Minute
)

// Cache only successful reads, shared by cards using the same connection. A
// changed token gets a different key; the cache never stores the token itself.
// Stale entries are returned immediately while a single background refresh
// updates the cache so 60 s card polls never block on GitHub.
var replyPRCache = struct {
	sync.Mutex
	entries    map[[32]byte]replyPRCacheEntry
	refreshing map[[32]byte]bool
	group      singleflight.Group
}{
	entries:    make(map[[32]byte]replyPRCacheEntry),
	refreshing: make(map[[32]byte]bool),
}

type replyPRCacheEntry struct {
	pr         *filesnapshots.PullRequest
	expires    time.Time
	staleUntil time.Time
}

func replyPRTTL(pr *filesnapshots.PullRequest) time.Duration {
	if pr != nil && (pr.State == "merged" || pr.State == "closed") {
		return replyPRSettledFreshTTL
	}
	return replyPRFreshTTL
}

func storeReplyPRCache(key [32]byte, pr *filesnapshots.PullRequest, now time.Time) {
	if pr != nil && (pr.State == "unavailable" || pr.Review == "unavailable") {
		return // The person's retry must not be held by a cached failure.
	}
	replyPRCache.Lock()
	defer replyPRCache.Unlock()
	for k, cached := range replyPRCache.entries {
		if now.After(cached.staleUntil) {
			delete(replyPRCache.entries, k)
		}
	}
	if len(replyPRCache.entries) >= 256 {
		for k := range replyPRCache.entries {
			delete(replyPRCache.entries, k)
			break
		}
	}
	replyPRCache.entries[key] = replyPRCacheEntry{
		pr:         pr,
		expires:    now.Add(replyPRTTL(pr)),
		staleUntil: now.Add(replyPRStaleTTL),
	}
}

func cachedGitHubPullRequestStatus(ctx context.Context, token, repo, branch string) *filesnapshots.PullRequest {
	baseURL := githubAPIURL
	key := sha256.Sum256([]byte(baseURL + "\x00" + token + "\x00" + repo + "\x00" + branch))
	now := time.Now()
	replyPRCache.Lock()
	entry, found := replyPRCache.entries[key]
	if found && now.Before(entry.expires) {
		replyPRCache.Unlock()
		return entry.pr
	}
	if found && now.Before(entry.staleUntil) {
		shouldRefresh := !replyPRCache.refreshing[key]
		if shouldRefresh {
			replyPRCache.refreshing[key] = true
		}
		replyPRCache.Unlock()
		if shouldRefresh {
			go func() {
				defer func() {
					replyPRCache.Lock()
					delete(replyPRCache.refreshing, key)
					replyPRCache.Unlock()
				}()
				bgCtx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
				defer cancel()
				pr := githubPullRequestStatusWithBase(bgCtx, baseURL, token, repo, branch)
				storeReplyPRCache(key, pr, time.Now())
			}()
		}
		return entry.pr
	}
	replyPRCache.Unlock()
	sfKey := hex.EncodeToString(key[:])
	value, _, _ := replyPRCache.group.Do(sfKey, func() (any, error) {
		replyPRCache.Lock()
		if cached, ok := replyPRCache.entries[key]; ok && time.Now().Before(cached.expires) {
			replyPRCache.Unlock()
			return cached.pr, nil
		}
		replyPRCache.Unlock()
		pr := githubPullRequestStatusWithBase(ctx, baseURL, token, repo, branch)
		storeReplyPRCache(key, pr, time.Now())
		return pr, nil
	})
	pr, _ := value.(*filesnapshots.PullRequest)
	return pr
}

func githubGET(ctx context.Context, token, path string, out any) bool {
	return githubGETWithBase(ctx, githubAPIURL, token, path, out)
}

func githubGETWithBase(ctx context.Context, baseURL, token, path string, out any) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+path, nil)
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
	return githubPullRequestStatusWithBase(ctx, githubAPIURL, token, repo, branch)
}

func githubPullRequestStatusWithBase(ctx context.Context, baseURL, token, repo, branch string) *filesnapshots.PullRequest {
	owner := strings.SplitN(repo, "/", 2)[0]
	var prs []githubPRHead
	q := url.Values{"head": {owner + ":" + branch}, "state": {"all"}, "sort": {"updated"}, "direction": {"desc"}, "per_page": {"1"}}
	if !githubGETWithBase(ctx, baseURL, token, "/repos/"+repo+"/pulls?"+q.Encode(), &prs) {
		return &filesnapshots.PullRequest{State: "unavailable"}
	}
	if len(prs) == 0 {
		return nil
	}
	return githubPullRequestHeadStatusWithBase(ctx, baseURL, token, repo, prs[0])
}

type githubPRHead struct {
	Number         int     `json:"number"`
	URL            string  `json:"html_url"`
	State          string  `json:"state"`
	MergedAt       *string `json:"merged_at"`
	Mergeable      *bool   `json:"mergeable"`
	MergeableState string  `json:"mergeable_state"`
	Head           struct {
		SHA string `json:"sha"`
		Ref string `json:"ref"`
	} `json:"head"`
}

func githubPullRequestHeadStatus(ctx context.Context, token, repo string, p githubPRHead) *filesnapshots.PullRequest {
	return githubPullRequestHeadStatusWithBase(ctx, githubAPIURL, token, repo, p)
}

func githubPullRequestHeadStatusWithBase(ctx context.Context, baseURL, token, repo string, p githubPRHead) *filesnapshots.PullRequest {
	pr := &filesnapshots.PullRequest{Number: p.Number, URL: p.URL, State: p.State, Checks: "none", Review: "none"}
	if p.MergedAt != nil {
		pr.State = "merged"
	}
	var runs struct {
		TotalCount int `json:"total_count"`
		CheckRuns  []struct {
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
	var reviews []struct {
		State string `json:"state"`
		User  struct {
			Login string `json:"login"`
		} `json:"user"`
	}
	var detail struct {
		MergeableState string `json:"mergeable_state"`
	}
	var runsOK, combinedOK, reviewsOK, detailOK bool
	prNum := strconv.Itoa(p.Number)
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		runsOK = githubGETWithBase(ctx, baseURL, token, "/repos/"+repo+"/commits/"+p.Head.SHA+"/check-runs?per_page=100", &runs)
	}()
	go func() {
		defer wg.Done()
		combinedOK = githubGETWithBase(ctx, baseURL, token, "/repos/"+repo+"/commits/"+p.Head.SHA+"/status", &combined)
	}()
	go func() {
		defer wg.Done()
		reviewsOK = githubGETWithBase(ctx, baseURL, token, "/repos/"+repo+"/pulls/"+prNum+"/reviews?per_page=100", &reviews)
	}()
	if pr.State == "open" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			detailOK = githubGETWithBase(ctx, baseURL, token, "/repos/"+repo+"/pulls/"+prNum, &detail)
		}()
	}
	wg.Wait()

	failed := func(name string) {
		if len(pr.FailingChecks) < replyGroupMaxFailingChecks {
			pr.FailingChecks = append(pr.FailingChecks, name)
		}
	}
	failing, pending, seen := false, false, false
	complete := true
	if runsOK {
		if runs.TotalCount > len(runs.CheckRuns) {
			complete = false
		}
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
	} else {
		complete = false
	}
	if !combinedOK {
		complete = false
	} else if combined.TotalCount > 0 {
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
	pr.PendingChecks = pending
	switch {
	case !complete:
		pr.Checks = "unavailable"
	case failing:
		pr.Checks = "failing"
	case pending:
		pr.Checks = "pending"
	case seen:
		pr.Checks = "passing"
	}
	if reviewsOK {
		latest := map[string]string{}
		commented := false
		for _, r := range reviews { // oldest first: the last decision per person wins
			commented = commented || r.State == "COMMENTED"
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
		if pr.Review == "none" && commented {
			pr.Review = "commented"
		}
	} else {
		pr.Review = "unavailable"
	}
	// Only the single-PR read says whether it can merge; "dirty" means conflicts.
	if pr.State == "open" && detailOK {
		pr.Conflicts = detail.MergeableState == "dirty"
	}
	return pr
}
