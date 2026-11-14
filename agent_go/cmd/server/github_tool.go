package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
)

// The github tool (PLAT-746): one small REST client over the GitHub login this
// workflow or Crew already has. The token is found exactly as the Crew's shell
// gets GITHUB_TOKEN (decisions log, 2026-10-30 and 2026-10-29): the workflow's
// selected secret, or the Crew's explicitly selected GitHub connection. A chat turn reads its own shell's selection, so the tool works in a
// turn exactly when that turn's shell has GITHUB_TOKEN. Nothing is widened.

var githubRepoPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

const githubToolReadLimit = 200 << 10

var githubAPIBase = "https://api.github.com"

const githubNotConnected = "No GitHub account is connected here. Connect GitHub in Vault (Settings → Vault → GitHub), then use it from this Crew, or select a GITHUB_TOKEN secret for this workflow."

// githubStepOwner is whose Vault a workflow step's GitHub login comes from: the person its shell takes secrets
// from (runSecretsOwner), never just whoever started the run. A Crew's owner wins for its workflows.
func (api *StreamingAPI) githubStepOwner(ctx context.Context, starter, workspace string) string {
	owner := api.runSecretsOwner(ctx, starter, workspace)
	crewRoot := workspace
	if binding, _ := findCrewExecutable(workspace); binding != nil {
		crewRoot = binding.CrewRoot
	}
	if crewOwner, ok := crewProjectOwnerID(crewRoot); ok {
		return crewOwner
	}
	return owner
}

func (api *StreamingAPI) githubToolToken(ctx context.Context) (string, error) {
	user := common.SessionUserIDFromContext(ctx)
	var secrets []resolvedSecret
	session, _ := ctx.Value(common.ChatSessionIDKey).(string)
	if value, ok := sessionSecretSources.Load(session); ok {
		// A chat turn: the very selection its shell's SECRET_* came from, so a turn
		// whose shell has no GITHUB_TOKEN gets none here either.
		source := value.(sessionSecretSource)
		secrets = sessionSecretsResolver(api, ctx, source)
		if owner := api.turnSecretsOwner(source.Session, source.User, source.Folder); owner != "" {
			user = owner
		}
	} else {
		// A workflow step: its saved selection, plus its Crew's explicitly selected
		// GitHub connection (productSelectedGlobalSecrets).
		workspace, err := common.GoogleWorkspaceFromContext(ctx)
		if err != nil || workspace == "" {
			return "", fmt.Errorf("Open this Crew or workflow's chat to use GitHub")
		}
		names := []string{}
		crewRoot := workspace
		if binding, _ := findCrewExecutable(workspace); binding != nil {
			crewRoot = binding.CrewRoot
		}
		if caps, found, err := LoadManifestForExecution(ctx, workspace); err == nil && found && caps.SelectedGlobalSecretNames != nil {
			names = append(names, (*caps.SelectedGlobalSecretNames)...)
		}
		// The step's shell takes its secrets from the project's owner, whoever started the run.
		user = api.githubStepOwner(ctx, user, workspace)
		if _, ok := crewProjectOwnerID(crewRoot); ok {
			if selected, err := productSelectedGlobalSecrets(ctx, crewProfileID, crewRoot); err == nil && selected != nil {
				names = append(names, (*selected)...)
			}
		}
		if user != "" && len(names) > 0 {
			secrets = api.mergeGlobalSecretsFor(ctx, user, &names)
		}
	}

	for _, secret := range secrets {
		if secret.Name == "GITHUB_TOKEN" && secret.Value != "" {
			return secret.Value, nil
		}
	}
	return "", fmt.Errorf("%s", githubNotConnected)
}

func (api *StreamingAPI) githubFromTool(ctx context.Context, args map[string]interface{}) (string, error) {
	action, repo := stringFromRequestMap(args, "action"), stringFromRequestMap(args, "repo")
	if !githubRepoPattern.MatchString(repo) {
		return "", fmt.Errorf("repo must be owner/name")
	}
	token, err := api.githubToolToken(ctx)
	if err != nil {
		return "", err
	}
	number, _ := args["number"].(float64)
	path := strings.Trim(stringFromRequestMap(args, "path"), "/")
	ref := url.Values{}
	if r := stringFromRequestMap(args, "ref"); r != "" {
		ref.Set("ref", r)
	}
	state := stringFromRequestMap(args, "state")
	if state == "" {
		state = "open"
	}
	need := func(fields ...string) error {
		var missing []string
		for _, field := range fields {
			if field == "number" && number < 1 || field != "number" && stringFromRequestMap(args, field) == "" {
				missing = append(missing, field)
			}
		}
		if len(missing) > 0 {
			return fmt.Errorf("%s needs %s", action, strings.Join(missing, ", "))
		}
		return nil
	}
	contents := "/repos/" + repo + "/contents/" + (&url.URL{Path: path}).EscapedPath()
	switch action {
	case "files":
		var items []struct {
			Name string `json:"name"`
			Path string `json:"path"`
			Type string `json:"type"`
			Size int    `json:"size"`
		}
		if err := githubJSON(ctx, token, http.MethodGet, contents+githubQuery(ref), nil, &items); err != nil {
			return "", err
		}
		return encodeGitHubReply(map[string]any{"files": items})
	case "read":
		if err := need("path"); err != nil {
			return "", err
		}
		var file struct {
			Content  string `json:"content"`
			Encoding string `json:"encoding"`
			Size     int    `json:"size"`
		}
		if err := githubJSON(ctx, token, http.MethodGet, contents+githubQuery(ref), nil, &file); err != nil {
			return "", err
		}
		if file.Encoding != "base64" || file.Size > githubToolReadLimit {
			return "", fmt.Errorf("%s is too large or not a text file to read here (limit 200 KB)", path)
		}
		data, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(file.Content, "\n", ""))
		if err != nil {
			return "", fmt.Errorf("GitHub returned an unreadable file")
		}
		return string(data), nil
	case "issues", "prs":
		kind := "issues"
		if action == "prs" {
			kind = "pulls"
		}
		var items []map[string]any
		if err := githubJSON(ctx, token, http.MethodGet, "/repos/"+repo+"/"+kind+"?per_page=50&state="+url.QueryEscape(state), nil, &items); err != nil {
			return "", err
		}
		out := []map[string]any{}
		for _, item := range items {
			if action == "issues" && item["pull_request"] != nil {
				continue // the issues list also returns pull requests
			}
			user, _ := item["user"].(map[string]any)
			out = append(out, map[string]any{"number": item["number"], "title": item["title"], "state": item["state"], "url": item["html_url"], "author": user["login"], "updated_at": item["updated_at"]})
		}
		return encodeGitHubReply(map[string]any{action: out})
	case "issue_create", "pr_create":
		body := map[string]any{"title": stringFromRequestMap(args, "title"), "body": stringFromRequestMap(args, "body")}
		route := "/repos/" + repo + "/issues"
		if err := need("title"); err != nil {
			return "", err
		}
		if action == "pr_create" {
			if err := need("head", "base"); err != nil {
				return "", err
			}
			body["head"], body["base"], route = stringFromRequestMap(args, "head"), stringFromRequestMap(args, "base"), "/repos/"+repo+"/pulls"
		}
		var created map[string]any
		if err := githubJSON(ctx, token, http.MethodPost, route, body, &created); err != nil {
			return "", err
		}
		return encodeGitHubReply(map[string]any{"number": created["number"], "url": created["html_url"]})
	case "comment":
		if err := need("number", "body"); err != nil {
			return "", err
		}
		var created map[string]any
		if err := githubJSON(ctx, token, http.MethodPost, fmt.Sprintf("/repos/%s/issues/%d/comments", repo, int(number)), map[string]any{"body": stringFromRequestMap(args, "body")}, &created); err != nil {
			return "", err
		}
		return encodeGitHubReply(map[string]any{"url": created["html_url"]})
	case "pr_diff":
		if err := need("number"); err != nil {
			return "", err
		}
		diff, err := githubRequest(ctx, token, http.MethodGet, fmt.Sprintf("/repos/%s/pulls/%d", repo, int(number)), nil, "application/vnd.github.diff")
		if err != nil {
			return "", err
		}
		if len(diff) > githubToolReadLimit {
			return string(diff[:githubToolReadLimit]) + "\n[diff cut at 200 KB; read changed files with action=read]", nil
		}
		return string(diff), nil
	}
	return "", fmt.Errorf("action must be files, read, issues, issue_create, comment, prs, pr_create or pr_diff")
}

func githubQuery(values url.Values) string {
	if len(values) == 0 {
		return ""
	}
	return "?" + values.Encode()
}

func encodeGitHubReply(value any) (string, error) {
	data, err := json.Marshal(value)
	return string(data), err
}

func githubJSON(ctx context.Context, token, method, route string, body, out any) error {
	data, err := githubRequest(ctx, token, method, route, body, "application/vnd.github+json")
	if err != nil {
		return err
	}
	if json.Unmarshal(data, out) != nil {
		return fmt.Errorf("GitHub returned an unexpected reply")
	}
	return nil
}

func githubRequest(ctx context.Context, token, method, route string, body any, accept string) ([]byte, error) {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(encoded)
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, githubAPIBase+route, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", accept)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GitHub could not be reached: %v", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, githubToolReadLimit+1))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		var failure struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(data, &failure)
		switch resp.StatusCode {
		case http.StatusUnauthorized:
			return nil, fmt.Errorf("the connected GitHub login was refused; reconnect GitHub in Vault")
		case http.StatusNotFound:
			return nil, fmt.Errorf("not found on GitHub, or the connected account cannot see it")
		}
		return nil, fmt.Errorf("GitHub said %d: %s", resp.StatusCode, failure.Message)
	}
	return data, nil
}
