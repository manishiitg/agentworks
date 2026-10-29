package server

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/filesnapshots"
)

// Branch / PR review (PLAT-746-200): what the current branch adds on top of
// its base, the same view as a pull request's "Files changed":
// git diff <merge-base(base, HEAD)> HEAD. The compare response pins both
// commits, and the per-file diff takes those exact commits, so a refresh in
// between cannot mix two versions.
//
// The default base is the open pull request's target branch, else the repo's
// default branch (origin/HEAD, then main, then master). The branch's own
// upstream (origin/<same branch>) is never consulted: comparing with it would
// hide everything already pushed.

// workspaceGitRefRe accepts plain branch names (main, origin/main, feature/x);
// option-like or revision-expression input is refused before git sees it.
var workspaceGitRefRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,199}$`)

type workspaceGitBranchFile struct {
	Path string `json:"path"`
	// Status is added, deleted or modified.
	Status  string `json:"status"`
	Added   int    `json:"added"`
	Removed int    `json:"removed"`
	Binary  bool   `json:"binary,omitempty"`
}

type workspaceGitPullRequest struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
	// Base is the branch the pull request merges into.
	Base string `json:"base"`
}

type workspaceGitBranchCompare struct {
	// Branch is the checked-out branch ("" when no branch is checked out).
	Branch string `json:"branch"`
	// Base is the branch compared with (as named in the repo, e.g. origin/main).
	Base string `json:"base"`
	// BaseSHA is the merge-base of Base and HEAD; HeadSHA is HEAD.
	BaseSHA string `json:"base_sha,omitempty"`
	HeadSHA string `json:"head_sha,omitempty"`
	// Unpushed counts commits on this branch that are on no remote.
	Unpushed  int                      `json:"unpushed"`
	Files     []workspaceGitBranchFile `json:"files"`
	Truncated bool                     `json:"truncated,omitempty"`
	PR        *workspaceGitPullRequest `json:"pr,omitempty"`
	// Note explains, in plain words, why nothing could be compared.
	Note string `json:"note,omitempty"`
}

func workspaceGitOutput(ctx context.Context, dir string, args ...string) (string, bool) {
	out, err := workspaceGitCommand(ctx, dir, args...).Output()
	return strings.TrimSpace(string(out)), err == nil
}

// workspaceGitResolveRef returns the commit a branch name points at ("" when
// the name is invalid or unknown).
func workspaceGitResolveRef(ctx context.Context, dir, ref string) string {
	if !workspaceGitRefRe.MatchString(ref) || strings.Contains(ref, "..") || strings.HasSuffix(ref, ".lock") {
		return ""
	}
	sha, ok := workspaceGitOutput(ctx, dir, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	if !ok || !workspaceGitCommitRe.MatchString(sha) {
		return ""
	}
	return sha
}

// workspaceGitDefaultBase picks the base: the pull request's target, else the
// default branch. It never returns the checked-out branch itself, and the
// branch's upstream is not a candidate.
func workspaceGitDefaultBase(ctx context.Context, dir, branch string, pr *workspaceGitPullRequest) (string, string) {
	candidates := []string{}
	if pr != nil && pr.Base != "" {
		candidates = append(candidates, "origin/"+pr.Base, pr.Base)
	}
	if head, ok := workspaceGitOutput(ctx, dir, "symbolic-ref", "--short", "-q", "refs/remotes/origin/HEAD"); ok && head != "" {
		candidates = append(candidates, head)
	}
	candidates = append(candidates, "origin/main", "main", "origin/master", "master")
	for _, candidate := range candidates {
		if candidate == branch {
			continue
		}
		if sha := workspaceGitResolveRef(ctx, dir, candidate); sha != "" {
			return candidate, sha
		}
	}
	return "", ""
}

// workspaceGitBranchPullRequest finds the open pull request for branch with
// the Crew owner's connected GitHub account (nil when there is none, the repo
// is not on GitHub, or GitHub does not answer).
func workspaceGitBranchPullRequest(ctx context.Context, dir, branch, githubOwner string) *workspaceGitPullRequest {
	if branch == "" || githubOwner == "" {
		return nil
	}
	remote, ok := workspaceGitOutput(ctx, dir, "config", "--get", "remote.origin.url")
	repo := filesnapshots.GitHubRepo(remote)
	if !ok || repo == "" {
		return nil
	}
	rec := readGitHubRecord(githubOwner)
	if rec == nil {
		return nil
	}
	token := githubTokenValue(rec)
	if token == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var prs []struct {
		Number int    `json:"number"`
		URL    string `json:"html_url"`
		Base   struct {
			Ref string `json:"ref"`
		} `json:"base"`
	}
	q := url.Values{"head": {strings.SplitN(repo, "/", 2)[0] + ":" + branch}, "state": {"open"}, "per_page": {"1"}}
	if !githubGET(ctx, token, "/repos/"+repo+"/pulls?"+q.Encode(), &prs) || len(prs) == 0 {
		return nil
	}
	pr := &workspaceGitPullRequest{Number: prs[0].Number, URL: prs[0].URL}
	if workspaceGitRefRe.MatchString(prs[0].Base.Ref) {
		pr.Base = prs[0].Base.Ref
	}
	if !strings.HasPrefix(pr.URL, "https://github.com/") {
		pr.URL = ""
	}
	return pr
}

// workspaceGitBranchFiles lists the files changed between two commits with
// their added/removed line counts (renames show as a delete and an add).
func workspaceGitBranchFiles(ctx context.Context, dir, base, head string) ([]workspaceGitBranchFile, bool, error) {
	names, err := workspaceGitCommand(ctx, dir, "diff", "--name-status", "-z", "--no-renames", "--no-ext-diff", "--no-textconv", base, head).Output()
	if err != nil {
		return nil, false, err
	}
	numstat, err := workspaceGitCommand(ctx, dir, "diff", "--numstat", "-z", "--no-renames", "--no-ext-diff", "--no-textconv", base, head).Output()
	if err != nil {
		return nil, false, err
	}
	type counts struct {
		added, removed int
		binary         bool
	}
	lines := map[string]counts{}
	for _, record := range bytes.Split(numstat, []byte{0}) {
		parts := strings.SplitN(string(record), "\t", 3)
		if len(parts) != 3 {
			continue
		}
		added, errA := strconv.Atoi(parts[0])
		removed, errR := strconv.Atoi(parts[1])
		lines[parts[2]] = counts{added, removed, errA != nil || errR != nil}
	}
	files := []workspaceGitBranchFile{}
	fields := bytes.Split(names, []byte{0})
	for i := 0; i+1 < len(fields); i += 2 {
		code, path := string(fields[i]), string(fields[i+1])
		if code == "" || path == "" {
			continue
		}
		if len(files) >= workspaceGitMaxFiles {
			return files, true, nil
		}
		status := "modified"
		switch code[0] {
		case 'A':
			status = "added"
		case 'D':
			status = "deleted"
		}
		c := lines[path]
		files = append(files, workspaceGitBranchFile{Path: path, Status: status, Added: c.added, Removed: c.removed, Binary: c.binary})
	}
	return files, false, nil
}

func serveWorkspaceGitBranchCompare(w http.ResponseWriter, ctx context.Context, repoDir, base, githubOwner string) {
	base = strings.TrimSpace(base)
	if base != "" && (!workspaceGitRefRe.MatchString(base) || strings.Contains(base, "..")) {
		writeWorkspaceGitJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid base"})
		return
	}
	result := workspaceGitBranchCompare{Files: []workspaceGitBranchFile{}}
	result.Branch, _ = workspaceGitOutput(ctx, repoDir, "symbolic-ref", "--short", "-q", "HEAD")
	head := workspaceGitResolveRef(ctx, repoDir, "HEAD")
	if head == "" {
		result.Base = base
		result.Note = "This project has no commits yet. Choose Uncommitted to see its changes."
		writeWorkspaceGitJSON(w, http.StatusOK, result)
		return
	}
	result.HeadSHA = head
	result.PR = workspaceGitBranchPullRequest(ctx, repoDir, result.Branch, githubOwner)
	baseCommit := ""
	if base != "" {
		result.Base, baseCommit = base, workspaceGitResolveRef(ctx, repoDir, base)
		if baseCommit == "" {
			result.Note = "The branch " + base + " was not found in this project. Choose another one."
		}
	} else {
		result.Base, baseCommit = workspaceGitDefaultBase(ctx, repoDir, result.Branch, result.PR)
		if baseCommit == "" {
			result.Note = "There is no other branch to compare with yet. Choose Uncommitted to see changes not saved in a commit."
		}
	}
	if remotes, _ := workspaceGitOutput(ctx, repoDir, "remote"); remotes != "" {
		if n, ok := workspaceGitOutput(ctx, repoDir, "rev-list", "--count", head, "--not", "--remotes"); ok {
			result.Unpushed, _ = strconv.Atoi(n)
		}
	}
	if baseCommit == "" {
		writeWorkspaceGitJSON(w, http.StatusOK, result)
		return
	}
	mergeBase, ok := workspaceGitOutput(ctx, repoDir, "merge-base", baseCommit, head)
	if !ok || !workspaceGitCommitRe.MatchString(mergeBase) {
		result.Note = "This branch shares no history with " + result.Base + ". Choose another branch to compare with."
		writeWorkspaceGitJSON(w, http.StatusOK, result)
		return
	}
	result.BaseSHA = mergeBase
	files, truncated, err := workspaceGitBranchFiles(ctx, repoDir, mergeBase, head)
	if err != nil {
		writeWorkspaceGitJSON(w, http.StatusInternalServerError, map[string]string{"error": "git failed"})
		return
	}
	result.Files, result.Truncated = files, truncated
	writeWorkspaceGitJSON(w, http.StatusOK, result)
}

func serveWorkspaceGitBranchDiff(w http.ResponseWriter, ctx context.Context, repoDir, file, baseSHA, headSHA string) {
	if file == "" || !workspaceGitCommitRe.MatchString(baseSHA) || !workspaceGitCommitRe.MatchString(headSHA) {
		writeWorkspaceGitJSON(w, http.StatusBadRequest, map[string]string{"error": "file, base_sha and head_sha are required"})
		return
	}
	out, err := workspaceGitCommand(ctx, repoDir, "diff", "--no-ext-diff", "--no-textconv", "--no-color", baseSHA, headSHA, "--", file).Output()
	if err != nil {
		writeWorkspaceGitJSON(w, http.StatusNotFound, map[string]string{"error": "This comparison is no longer available. Refresh and try again."})
		return
	}
	truncated := false
	if len(out) > workspaceGitMaxDiffBytes {
		out, truncated = out[:workspaceGitMaxDiffBytes], true
	}
	writeWorkspaceGitJSON(w, http.StatusOK, map[string]any{"diff": string(out), "truncated": truncated})
}

// workspaceGitUntrackedDiff shows a new, not yet added file as all-added lines
// (git diff against HEAD leaves it out). Only a regular file git reports as
// untracked is read.
func workspaceGitUntrackedDiff(ctx context.Context, repoDir, file string) []byte {
	listed, err := workspaceGitCommand(ctx, repoDir, "ls-files", "--others", "--exclude-standard", "-z", "--", file).Output()
	if err != nil || string(bytes.TrimRight(listed, "\x00")) != file {
		return nil
	}
	full := filepath.Join(repoDir, filepath.FromSlash(file))
	if info, err := os.Lstat(full); err != nil || !info.Mode().IsRegular() {
		return nil
	}
	if _, ok := workspaceGitConfined(repoDir, full); !ok {
		return nil
	}
	out, err := workspaceGitCommand(ctx, repoDir, "diff", "--no-index", "--no-ext-diff", "--no-textconv", "--no-color", "--", os.DevNull, file).Output()
	var exit *exec.ExitError
	if err != nil && !(errors.As(err, &exit) && exit.ExitCode() == 1) {
		return nil
	}
	return out
}
