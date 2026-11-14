package server

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"os/exec"
	"strconv"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/filesnapshots"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// Source Control delivery always uses the explicitly selected project account.
// The repo supplies only a GitHub owner/name, never a transport, command or URL
// to which the saved credential could be sent. No operator gh/SSH login fallback.
type workspaceGitDeliveryTarget struct {
	Repository string
	Branch     string
	Head       string
	token      string
}

func workspaceGitDeliveryResolve(ctx context.Context, r *http.Request, canonical, dir string) (*workspaceGitDeliveryTarget, error) {
	if externalFileGuard(ctx, false) != nil {
		return nil, errors.New("Open this project in the app to push or open a pull request. A folder-limited connection cannot publish its whole repository.")
	}
	roots, found, err := crewroot.Lookup(workspaceGitDocsRoot(), canonical)
	if err != nil || !found || roots.Virtual != canonical || roots.Owner == "" {
		return nil, errors.New("Open a Crew and choose its GitHub connection before publishing commits.")
	}
	if !workspaceGitWriteAllowed(ctx, r, GetUserFromContext(ctx), canonical) {
		return nil, errors.New("Only this project's Builder can publish its commits.")
	}
	remote, ok := workspaceGitOutput(ctx, dir, "config", "--get", "remote.origin.url")
	repository := filesnapshots.GitHubRepo(remote)
	if !ok || repository == "" {
		return nil, errors.New("This action needs a GitHub origin remote. Ask the agent to check this repository's remote.")
	}
	branch, ok := workspaceGitOutput(ctx, dir, "symbolic-ref", "--quiet", "--short", "HEAD")
	if !ok || !workspaceGitBranchName(ctx, dir, branch) {
		return nil, errors.New("Choose a local branch before pushing or opening a pull request.")
	}
	head, ok := workspaceGitOutput(ctx, dir, "rev-parse", "--verify", "HEAD")
	if !ok || !workspaceGitCommitRe.MatchString(head) {
		return nil, errors.New("Make a commit before publishing this branch.")
	}
	rec, err := projectGitHubConnection(ctx, roots.Owner, canonical)
	if err != nil {
		return nil, err
	}
	if rec == nil || githubTokenValue(rec) == "" {
		return nil, errors.New("Connect GitHub for this Crew in Connections before pushing or opening a pull request.")
	}
	return &workspaceGitDeliveryTarget{Repository: repository, Branch: branch, Head: head, token: githubTokenValue(rec)}, nil
}

// Before enabling HTTPS, reject repository settings that can rewrite the
// pinned GitHub URL, change its TLS/proxy/auth handling or request a custom
// transport/push command. All the ordinary safegit protections remain in place.
func workspaceGitDeliveryConfigAllowed(keys string) bool {
	for _, raw := range strings.Split(keys, "\n") {
		key := strings.ToLower(strings.TrimSpace(raw))
		for _, prefix := range []string{"url.", "http.", "https.", "credential.", "push.", "include.", "includeif.", "protocol."} {
			if strings.HasPrefix(key, prefix) {
				return false
			}
		}
		if key == "core.gitproxy" {
			return false
		}
		if strings.HasPrefix(key, "remote.") {
			for _, suffix := range []string{".pushurl", ".proxy", ".receivepack", ".uploadpack", ".vcs", ".mirror", ".pushoption"} {
				if strings.HasSuffix(key, suffix) {
					return false
				}
			}
		}
	}
	return true
}

// Tests substitute only the actual external network process, not admission,
// command construction, local Git status or local tracking updates.
var workspaceGitDeliveryRunForTest func(*exec.Cmd) error

func workspaceGitDeliveryTokenAllowed(r *http.Request, canonical string) bool {
	if externalFileGuard(r.Context(), false) != nil {
		return false
	}
	claims := GetUserFromContext(r.Context())
	if claims == nil || claims.AccessToken == nil {
		return true
	}
	token := claims.AccessToken
	roots, found, err := crewroot.Lookup(workspaceGitDocsRoot(), canonical)
	return found && err == nil && token.ProjectKind == "" && token.BoundAgentID == "" && token.BoundAgentName == "" && token.Allows("crews:write") && token.AllowsCrew(roots.ID)
}

func workspaceGitDeliveryPush(ctx context.Context, dir string, target *workspaceGitDeliveryTarget) error {
	if target == nil || !githubRepoPattern.MatchString(target.Repository) || !workspaceGitBranchName(ctx, dir, target.Branch) || target.token == "" {
		return errors.New("Choose this project's connected GitHub account and a local branch.")
	}
	keys, ok := workspaceGitOutput(ctx, dir, "config", "--local", "--name-only", "--list")
	if !ok || !workspaceGitDeliveryConfigAllowed(keys) {
		return errors.New("This repository has custom network or push settings. Ask the agent to review them before pushing from the panel.")
	}
	// A literal HTTPS URL and explicit non-force refspec: never invoke the repo's
	// push remote, receive-pack, helpers, hooks, signing or submodule recursion.
	// Scoped auth stays in the child environment, not argv, a URL or a file.
	cmd := workspaceGitCommand(ctx, dir,
		"-c", "protocol.https.allow=always",
		"-c", "credential.helper=", "-c", "core.askPass=",
		"-c", "http.proxy=", "-c", "http.sslVerify=true", "-c", "http.followRedirects=false",
		"-c", "push.gpgSign=false", "-c", "push.recurseSubmodules=no",
		"push", "--porcelain", "--no-verify", "--recurse-submodules=no", "--",
		"https://github.com/"+target.Repository+".git", target.Head+":refs/heads/"+target.Branch)
	auth := "Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte("x-access-token:"+target.token))
	cmd.Env = append(cmd.Env, "GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=http.https://github.com/.extraHeader", "GIT_CONFIG_VALUE_0="+auth)
	// Neither git stderr nor exec errors are safe UI/log text: they may contain
	// remote credentials or repository-controlled material. Status is enough.
	var runErr error
	if workspaceGitDeliveryRunForTest != nil {
		runErr = workspaceGitDeliveryRunForTest(cmd)
	} else {
		runErr = cmd.Run()
	}
	if runErr != nil {
		return errors.New("GitHub did not accept the push. Check the selected account's repository access and whether the remote branch has newer commits. Nothing was force-pushed.")
	}
	// Keep ordinary status/ahead counts and the next Push on this branch.
	if _, err := workspaceGitRunWrite(ctx, dir, nil, "update-ref", "refs/remotes/origin/"+target.Branch, target.Head); err != nil {
		return errors.New("The push succeeded, but the local tracking reference could not refresh. Refresh Source Control before another action.")
	}
	if _, err := workspaceGitRunWrite(ctx, dir, nil, "branch", "--set-upstream-to=origin/"+target.Branch, target.Branch); err != nil {
		return errors.New("The push succeeded, but tracking could not be saved. Ask the agent to check this branch before pushing again.")
	}
	return nil
}

func workspaceGitDeliveryOpenPR(ctx context.Context, target *workspaceGitDeliveryTarget, base, title, body string) (*workspaceGitPullRequest, error) {
	if target == nil || !githubRepoPattern.MatchString(target.Repository) || !workspaceGitRefRe.MatchString(base) || strings.Contains(base, "..") || base == target.Branch || strings.TrimSpace(title) == "" || len(title) > 256 || len(body) > 20000 {
		return nil, errors.New("Choose a different base branch and check the pull request title and description.")
	}
	// Do not silently push or publish a different remote revision. The title and
	// body have been reviewed by the person; opening is a separate explicit action.
	var remote struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	if err := githubJSON(ctx, target.token, http.MethodGet, "/repos/"+target.Repository+"/git/ref/heads/"+target.Branch, nil, &remote); err != nil || remote.Object.SHA != target.Head {
		return nil, errors.New("Push this branch's current commits first, then open its pull request.")
	}
	var created struct {
		Number int    `json:"number"`
		URL    string `json:"html_url"`
	}
	if err := githubJSON(ctx, target.token, http.MethodPost, "/repos/"+target.Repository+"/pulls", map[string]any{"head": target.Branch, "base": base, "title": title, "body": body}, &created); err != nil {
		return nil, errors.New("GitHub could not open the pull request. Check whether one already exists and that this account can open pull requests in the repository.")
	}
	if created.Number < 1 || created.URL == "" {
		return nil, errors.New("GitHub returned no pull request confirmation. Check the repository before trying again.")
	}
	return &workspaceGitPullRequest{Number: created.Number, URL: "https://github.com/" + target.Repository + "/pull/" + strconv.Itoa(created.Number), Base: base}, nil
}
