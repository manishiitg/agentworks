package server

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/filesnapshots"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// The pull request panel (#2922 task 3): a chat's own pull request read from
// GitHub with the Crew owner's connected account, like the reply card's PR
// status. Only repositories this chat worked in (its saved reply groups) or
// pull requests its turns linked can be read, so a chat reader never browses
// other repositories through the owner's login. Merging stays with the agent:
// the panel only offers to ask it, and the agent asks the person how to merge.

const (
	prReviewMaxFiles     = 100
	prReviewMaxComments  = 100
	prReviewMaxPatch     = 200 << 10
	prReviewLinkedEvents = 5000
)

type prReviewFile struct {
	Path         string `json:"path"`
	PreviousPath string `json:"previous_path,omitempty"`
	Status       string `json:"status"`
	Additions    int    `json:"additions"`
	Deletions    int    `json:"deletions"`
	Patch        string `json:"patch,omitempty"`
}

type prReviewComment struct {
	Path     string `json:"path,omitempty"`
	Line     int    `json:"line,omitempty"`
	Outdated bool   `json:"outdated,omitempty"`
	Author   string `json:"author"`
	Body     string `json:"body"`
	URL      string `json:"url,omitempty"`
	At       string `json:"at,omitempty"`
	Reply    bool   `json:"reply,omitempty"`
	Review   string `json:"review,omitempty"` // a review's own summary: approved, changes_requested or commented
}

type prReview struct {
	Repository string                     `json:"repository"`
	Number     int                        `json:"number"`
	URL        string                     `json:"url"`
	Title      string                     `json:"title"`
	Body       string                     `json:"body,omitempty"`
	Author     string                     `json:"author,omitempty"`
	Draft      bool                       `json:"draft,omitempty"`
	Head       string                     `json:"head"`
	Base       string                     `json:"base"`
	Additions  int                        `json:"additions"`
	Deletions  int                        `json:"deletions"`
	FileCount  int                        `json:"file_count"`
	Status     *filesnapshots.PullRequest `json:"status"`
	Mergeable  bool                       `json:"mergeable"`
	Files      []prReviewFile             `json:"files"`
	MoreFiles  bool                       `json:"more_files,omitempty"`
	Comments   []prReviewComment          `json:"comments"`
	CanAsk     bool                       `json:"can_ask"`
}

// chatPullRequestRepos are the GitHub repositories this chat may read pull
// requests from, lowercased: those of its saved reply groups and of the pull
// requests its turns linked.
func (api *StreamingAPI) chatPullRequestRepos(history chatFileReview, sessionID, root string) map[string]bool {
	repos := map[string]bool{}
	for _, ref := range history.Replies {
		groups, _ := history.summaryGroups(ref.ID)
		for _, group := range groups {
			if group.Kind == "repo" && group.GitHubRepo != "" {
				repos[strings.ToLower(group.GitHubRepo)] = true
			}
		}
	}
	linked := append([]json.RawMessage(nil), history.linkedPRs...)
	if api.eventStore != nil { // a just-finished turn may not be journaled yet
		for _, event := range api.eventStore.LastEventsRaw(sessionID, prReviewLinkedEvents) {
			if event.Type != "reply_pull_requests" || event.Data == nil {
				continue
			}
			if raw, err := json.Marshal(event.Data.Data); err == nil {
				linked = append(linked, raw)
			}
		}
	}
	for _, raw := range linked {
		var data struct {
			Workspace    string              `json:"workspace_path"`
			PullRequests []linkedPullRequest `json:"pull_requests"`
		}
		if json.Unmarshal(raw, &data) != nil || (data.Workspace != "" && data.Workspace != root) {
			continue
		}
		for _, pr := range data.PullRequests {
			repos[strings.ToLower(pr.Repository)] = true
		}
	}
	return repos
}

func validGitHubRepo(repo string) bool {
	parts := strings.Split(repo, "/")
	if len(parts) != 2 {
		return false
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || len(part) > 100 {
			return false
		}
		for _, c := range part {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
				return false
			}
		}
	}
	return true
}

func (api *StreamingAPI) servePullRequestReview(w http.ResponseWriter, r *http.Request, roots crewroot.Roots, history chatFileReview, sessionID string) {
	repo := strings.TrimSpace(r.URL.Query().Get("repository"))
	number, err := strconv.Atoi(r.URL.Query().Get("number"))
	if !validGitHubRepo(repo) || err != nil || number <= 0 {
		externalError(w, 400, "invalid_pull_request", "Choose a pull request from this chat.")
		return
	}
	if !api.chatPullRequestRepos(history, sessionID, roots.Virtual)[strings.ToLower(repo)] {
		log.Printf("[PR_REVIEW] session=%s repo=%s pr=%d refused=not_this_chat", sessionID, repo, number)
		externalError(w, 404, "not_found", "This chat has no pull request in that repository.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	token := ""
	if rec, _ := projectGitHubConnection(ctx, roots.Owner, roots.Virtual); rec != nil {
		token = githubTokenValue(rec)
	}
	if token == "" {
		externalError(w, 409, "connect_github", "Connect GitHub in this project to see the pull request here.")
		return
	}
	out, ok := githubPullRequestReview(ctx, token, repo, number)
	if !ok {
		log.Printf("[PR_REVIEW] session=%s repo=%s pr=%d lookup=unavailable", sessionID, repo, number)
		externalError(w, 502, "github_unavailable", "GitHub did not answer. Try again.")
		return
	}
	out.CanAsk = history.CanReview
	log.Printf("[PR_REVIEW] session=%s repo=%s pr=%d state=%s checks=%s review=%s files=%d comments=%d mergeable=%t", sessionID, repo, number, out.Status.State, out.Status.Checks, out.Status.Review, len(out.Files), len(out.Comments), out.Mergeable)
	externalJSON(w, out)
}

func githubPullRequestReview(ctx context.Context, token, repo string, number int) (prReview, bool) {
	var p struct {
		githubPRHead
		Title     string `json:"title"`
		Body      string `json:"body"`
		Draft     bool   `json:"draft"`
		Additions int    `json:"additions"`
		Deletions int    `json:"deletions"`
		Changed   int    `json:"changed_files"`
		User      struct {
			Login string `json:"login"`
		} `json:"user"`
		Base struct {
			Ref string `json:"ref"`
		} `json:"base"`
	}
	path := "/repos/" + repo + "/pulls/" + strconv.Itoa(number)
	if !githubGET(ctx, token, path, &p) || p.Number != number {
		return prReview{}, false
	}
	out := prReview{
		Repository: repo, Number: p.Number, URL: p.URL, Title: p.Title, Body: p.Body, Author: p.User.Login, Draft: p.Draft,
		Head: p.Head.Ref, Base: p.Base.Ref, Additions: p.Additions, Deletions: p.Deletions, FileCount: p.Changed,
		Files: []prReviewFile{}, Comments: []prReviewComment{},
	}
	out.Status = githubPullRequestHeadStatus(ctx, token, repo, p.githubPRHead)
	// GitHub's own answer: clean (and unstable or has_hooks, which still merge).
	out.Mergeable = out.Status.State == "open" && !p.Draft && p.Mergeable != nil && *p.Mergeable &&
		(p.MergeableState == "clean" || p.MergeableState == "unstable" || p.MergeableState == "has_hooks")

	var files []struct {
		Filename         string `json:"filename"`
		PreviousFilename string `json:"previous_filename"`
		Status           string `json:"status"`
		Additions        int    `json:"additions"`
		Deletions        int    `json:"deletions"`
		Patch            string `json:"patch"`
	}
	if githubGET(ctx, token, path+"/files?per_page="+strconv.Itoa(prReviewMaxFiles), &files) {
		budget := prReviewMaxPatch
		for _, f := range files {
			file := prReviewFile{Path: f.Filename, PreviousPath: f.PreviousFilename, Status: f.Status, Additions: f.Additions, Deletions: f.Deletions}
			if len(f.Patch) <= budget {
				file.Patch = f.Patch
				budget -= len(f.Patch)
			}
			out.Files = append(out.Files, file)
		}
		out.MoreFiles = p.Changed > len(files)
	}

	var reviews []struct {
		State string `json:"state"`
		Body  string `json:"body"`
		URL   string `json:"html_url"`
		At    string `json:"submitted_at"`
		User  struct {
			Login string `json:"login"`
		} `json:"user"`
	}
	if githubGET(ctx, token, path+"/reviews?per_page=100", &reviews) {
		for _, review := range reviews {
			if strings.TrimSpace(review.Body) == "" {
				continue
			}
			kind := map[string]string{"APPROVED": "approved", "CHANGES_REQUESTED": "changes_requested", "COMMENTED": "commented"}[review.State]
			if kind == "" {
				continue
			}
			out.Comments = append(out.Comments, prReviewComment{Author: review.User.Login, Body: review.Body, URL: review.URL, At: review.At, Review: kind})
		}
	}
	var comments []struct {
		Path         string `json:"path"`
		Line         *int   `json:"line"`
		OriginalLine *int   `json:"original_line"`
		Body         string `json:"body"`
		URL          string `json:"html_url"`
		At           string `json:"created_at"`
		InReplyTo    int    `json:"in_reply_to_id"`
		User         struct {
			Login string `json:"login"`
		} `json:"user"`
	}
	if githubGET(ctx, token, path+"/comments?per_page="+strconv.Itoa(prReviewMaxComments), &comments) {
		for _, c := range comments {
			comment := prReviewComment{Path: c.Path, Author: c.User.Login, Body: c.Body, URL: c.URL, At: c.At, Reply: c.InReplyTo != 0}
			switch {
			case c.Line != nil:
				comment.Line = *c.Line
			case c.OriginalLine != nil:
				comment.Line, comment.Outdated = *c.OriginalLine, true
			default:
				comment.Outdated = true
			}
			out.Comments = append(out.Comments, comment)
		}
	}
	return out, true
}
