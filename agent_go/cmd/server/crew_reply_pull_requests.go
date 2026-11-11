package server

import (
	"context"
	"encoding/json"
	"log"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	unifiedevents "github.com/manishiitg/mcpagent/events"
)

// Linking a turn's pull requests to its chat (PLAT-746). File changes are not
// needed: when a Crew turn ends, the server asks GitHub which pull requests in
// the repositories the turn's own tool calls named were opened or updated
// during the turn, and keeps those its tool calls also name (the PR link, or
// its branch next to its repository, as `git push` and `gh pr create` print).
// Several chats sharing one GitHub login therefore never claim each other's
// PRs. The agent's reply text is not read. Linking never starts a watcher.

const (
	replyPRMaxRepos   = 3
	replyPRMaxLinked  = 5
	replyPRTurnEvents = 1500
	replyPRClockSlack = 2 * time.Minute
)

var replyPRRepoMention = regexp.MustCompile(`(?i)github\.com[/:]([A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9_.-]+)`)

type linkedPullRequest struct {
	Repository string `json:"repository"`
	Number     int    `json:"number"`
	URL        string `json:"url"`
	Title      string `json:"title,omitempty"`
	Branch     string `json:"branch,omitempty"`
	State      string `json:"state"` // open, merged or closed
}

// replyTurnToolTexts returns each tool call's arguments and result as one
// lowercased text, for the turn's stored events after start up to end.
func (api *StreamingAPI) replyTurnToolTexts(sessionID string, start, end int64) []string {
	byCall := map[string]*strings.Builder{}
	var order []string
	for _, event := range api.eventStore.LastEventsRaw(sessionID, replyPRTurnEvents) {
		if event.Sequence <= start || event.Sequence > end || event.Data == nil {
			continue
		}
		if event.Type != "tool_call_start" && event.Type != "tool_call_end" {
			continue
		}
		raw, err := json.Marshal(event.Data)
		if err != nil {
			continue
		}
		var call struct {
			Data struct {
				ToolCallID string `json:"tool_call_id"`
			} `json:"data"`
		}
		_ = json.Unmarshal(raw, &call)
		key := firstNonEmptyString(call.Data.ToolCallID, event.ID)
		b, ok := byCall[key]
		if !ok {
			b = &strings.Builder{}
			byCall[key] = b
			order = append(order, key)
		}
		// JSON escapes a newline as \n; make it a separator again.
		b.WriteString(strings.ReplaceAll(strings.ToLower(string(raw)), `\n`, "\n"))
		b.WriteByte('\n')
	}
	texts := make([]string, 0, len(order))
	for _, key := range order {
		texts = append(texts, byCall[key].String())
	}
	return texts
}

// replyTurnRepos lists the GitHub repositories (owner/name, lowercase) the
// turn's tool calls named, most mentioned first.
func replyTurnRepos(texts []string) []string {
	counts := map[string]int{}
	for _, text := range texts {
		for _, m := range replyPRRepoMention.FindAllStringSubmatch(text, -1) {
			repo := strings.TrimSuffix(strings.TrimRight(m[1], "."), ".git")
			if parts := strings.SplitN(repo, "/", 2); len(parts) == 2 && parts[1] != "" && parts[1] != "." && parts[1] != ".." && githubRepoPattern.MatchString(repo) {
				counts[repo]++
			}
		}
	}
	repos := make([]string, 0, len(counts))
	for repo := range counts {
		repos = append(repos, repo)
	}
	sort.Slice(repos, func(i, j int) bool {
		if counts[repos[i]] != counts[repos[j]] {
			return counts[repos[i]] > counts[repos[j]]
		}
		return repos[i] < repos[j]
	})
	if len(repos) > replyPRMaxRepos {
		repos = repos[:replyPRMaxRepos]
	}
	return repos
}

func branchChar(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || b == '-' || b == '_' || b == '.' || b == '/'
}

// containsBranch reports whether branch occurs in text as a whole name, so
// branch "fix" does not match "fix-later". A ref path (refs/heads/<branch>)
// or GitHub's new-PR link (/pull/new/<branch>) also counts.
func containsBranch(text, branch string) bool {
	for from := 0; branch != ""; {
		i := strings.Index(text[from:], branch)
		if i < 0 {
			return false
		}
		i += from
		after := i + len(branch)
		before := i == 0 || !branchChar(text[i-1]) || strings.HasSuffix(text[:i], "refs/heads/") || strings.HasSuffix(text[:i], "/pull/new/")
		trailing := after >= len(text) || !branchChar(text[after]) || text[after] == '.' && (after+1 >= len(text) || !branchChar(text[after+1]))
		if before && trailing {
			return true
		}
		from = i + 1
	}
	return false
}

// containsPRLink reports whether text has this pull request's link, not
// another number that starts with the same digits.
func containsPRLink(text, link string) bool {
	for from := 0; ; {
		i := strings.Index(text[from:], link)
		if i < 0 {
			return false
		}
		after := from + i + len(link)
		if after >= len(text) || text[after] < '0' || text[after] > '9' {
			return true
		}
		from = after
	}
}

// replyTurnNamesPR reports whether one of the turn's tool calls names this
// pull request: its link, or its branch in a call that also names its
// repository (a push or PR command and its output).
func replyTurnNamesPR(texts []string, repo string, number int, branch string) bool {
	link := "github.com/" + repo + "/pull/" + strconv.Itoa(number)
	branch = strings.ToLower(branch)
	byBranch := branch != "" && branch != "main" && branch != "master"
	for _, text := range texts {
		if containsPRLink(text, link) {
			return true
		}
		namesRepo := strings.Contains(text, "github.com/"+repo) || strings.Contains(text, "github.com:"+repo)
		if byBranch && namesRepo && containsBranch(text, branch) {
			return true
		}
	}
	return false
}

// linkTurnPullRequests runs once per finished Crew turn, beside the file
// capture, and records the turn's pull requests as a reply_pull_requests
// event bound to the reply like reply_file_changes.
func (api *StreamingAPI) linkTurnPullRequests(snapshot *crewTurnSnapshot, roots crewroot.Roots) {
	if api.eventStore == nil {
		return
	}
	end, ok := api.crewSnapshotReplyEnd(snapshot)
	if !ok {
		return
	}
	texts := api.replyTurnToolTexts(snapshot.sessionID, snapshot.startSequence, end)
	repos := replyTurnRepos(texts)
	if len(repos) == 0 {
		return // no tool call named a GitHub repository: nothing to ask GitHub
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	rec, _ := projectGitHubConnection(ctx, roots.Owner, roots.Virtual)
	token := ""
	if rec != nil {
		token = githubTokenValue(rec)
	}
	if token == "" {
		log.Printf("[REPLY_PR] session=%s execution=%s repos=%s skipped=no_github_connection", snapshot.sessionID, snapshot.executionID, strings.Join(repos, ","))
		return
	}
	since := snapshot.sentAt.Add(-replyPRClockSlack)
	var linked []linkedPullRequest
	candidates := 0
	for _, repo := range repos {
		var prs []struct {
			Number    int       `json:"number"`
			URL       string    `json:"html_url"`
			Title     string    `json:"title"`
			State     string    `json:"state"`
			MergedAt  *string   `json:"merged_at"`
			UpdatedAt time.Time `json:"updated_at"`
			Head      struct {
				Ref string `json:"ref"`
			} `json:"head"`
		}
		q := url.Values{"state": {"all"}, "sort": {"updated"}, "direction": {"desc"}, "per_page": {"30"}}
		if !githubGET(ctx, token, "/repos/"+repo+"/pulls?"+q.Encode(), &prs) {
			log.Printf("[REPLY_PR] session=%s execution=%s repo=%s lookup=unavailable", snapshot.sessionID, snapshot.executionID, repo)
			continue
		}
		for _, pr := range prs {
			if pr.UpdatedAt.Before(since) {
				break // sorted newest update first
			}
			candidates++
			if len(linked) >= replyPRMaxLinked || !replyTurnNamesPR(texts, repo, pr.Number, pr.Head.Ref) {
				continue
			}
			state := pr.State
			if pr.MergedAt != nil {
				state = "merged"
			}
			// GitHub's own spelling of the repository, as a watcher saves it.
			name := repo
			if u, err := url.Parse(pr.URL); err == nil {
				if parts := strings.Split(strings.Trim(u.Path, "/"), "/"); len(parts) == 4 && strings.EqualFold(parts[0]+"/"+parts[1], repo) {
					name = parts[0] + "/" + parts[1]
				}
			}
			linked = append(linked, linkedPullRequest{Repository: name, Number: pr.Number, URL: pr.URL, Title: pr.Title, Branch: pr.Head.Ref, State: state})
		}
	}
	names := make([]string, 0, len(linked))
	for _, pr := range linked {
		names = append(names, pr.Repository+"#"+strconv.Itoa(pr.Number)+":"+pr.State)
	}
	log.Printf("[REPLY_PR] session=%s execution=%s repos=%s updated_in_turn=%d linked=%s start_sequence=%d end_sequence=%d", snapshot.sessionID, snapshot.executionID, strings.Join(repos, ","), candidates, firstNonEmptyString(strings.Join(names, ","), "none"), snapshot.startSequence, end)
	if len(linked) == 0 {
		return
	}
	data := storeevents.NewGenericEventData("reply_pull_requests", map[string]interface{}{
		"pull_requests": linked, "workspace_path": snapshot.root,
		"execution_id": snapshot.executionID, "start_sequence": snapshot.startSequence, "end_sequence": end,
	})
	event := unifiedevents.NewAgentEvent(data)
	event.SessionID = snapshot.sessionID
	api.eventStore.AddEvent(snapshot.sessionID, storeevents.Event{
		ID: "reply-prs:" + snapshot.executionID, Type: "reply_pull_requests", Timestamp: time.Now(), SessionID: snapshot.sessionID,
		ExecutionID: snapshot.executionID, ExecutionKind: "main_agent", TerminalOwnerID: "main:" + snapshot.sessionID, Data: event,
	})
}
