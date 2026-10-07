package knowledgebase

import (
	"context"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Brain's history is its Git folder (PLAT-633): every save through the tools and every direct edit Brain picks up is
// committed at once, authored as the person who made it. "What changed" is a commit range: list_knowledgebase_changes
// returns the head commit and the commits since a given one, limited to folders the caller can read, and
// read_knowledgebase_diff shows a note's changes over that range (owner, 2026-10-07: "i prefer commit id approach").
// Pushing to the backup remote stays a separate, explicit git push.

var commitID = regexp.MustCompile(`^[0-9a-f]{7,40}$`)

const maxChangeCommits = 200
const maxDiffBytes = 200 << 10

func (s *Service) gitInLive(ctx context.Context, env []string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = s.live
	cmd.Env = append(append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0"), env...)
	out, err := cmd.Output()
	return string(out), err
}

func (s *Service) hasGitHistory() bool {
	_, err := os.Stat(filepath.Join(s.live, ".git"))
	return err == nil
}

// recordHistory commits whatever changed in Brain's folder, as actor. Failures never fail the save: the change is
// already live and is committed with the next one.
func (s *Service) recordHistory(actor, message string) {
	if !s.hasGitHistory() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := s.gitInLive(ctx, nil, "add", "-A"); err != nil {
		return
	}
	if _, err := s.gitInLive(ctx, nil, "diff", "--cached", "--quiet"); err == nil {
		return // nothing staged
	}
	name := actor
	if ids, err := s.identities(); err == nil {
		if id, ok := ids[actor]; ok && strings.TrimSpace(id.Name) != "" {
			name = id.Name
		}
	}
	if strings.TrimSpace(name) == "" {
		name = "Brain"
	}
	email := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
			return r
		}
		return '-'
	}, actor) + "@brain.local"
	env := []string{"GIT_AUTHOR_NAME=" + name, "GIT_AUTHOR_EMAIL=" + email, "GIT_COMMITTER_NAME=" + name, "GIT_COMMITTER_EMAIL=" + email}
	_, _ = s.gitInLive(ctx, env, "commit", "-q", "--no-verify", "-m", message)
}

// historyMessage is a one-line commit message for a Brain save.
func historyMessage(tool string, a map[string]any) string {
	what := stringArg(a, "path")
	if what == "" {
		what = strings.Trim(path.Join(stringArg(a, "folder_path"), firstNonEmpty(stringArg(a, "filename"), stringArg(a, "name"))), "/")
	}
	if what == "" {
		what = stringArg(a, "entry_id")
	}
	verb := map[string]string{"create_knowledgebase": "Add", "update_knowledgebase": "Update", "delete_knowledgebase": "Delete", "create_knowledgebase_folder": "Add folder", "publish_knowledgebase_skill": "Publish skill"}[tool]
	if verb == "" {
		return ""
	}
	return strings.TrimSpace(verb + " " + what)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// readableNotePath reports whether p may see a note path's folder.
func (s *Service) readableNotePath(p Principal, notePath string) bool {
	folder := path.Dir(notePath)
	if folder == "." {
		folder = ""
	}
	return s.effective(p, folder) >= roleReader
}

func (s *Service) headCommit(ctx context.Context) string {
	out, err := s.gitInLive(ctx, nil, "rev-parse", "--verify", "-q", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

func (s *Service) listChanges(ctx context.Context, p Principal, a map[string]any) (any, error) {
	if !s.hasGitHistory() {
		return nil, kbErr("UNAVAILABLE", "Brain's history is not set up on this server yet.")
	}
	head := s.headCommit(ctx)
	since := strings.TrimSpace(stringArg(a, "since"))
	limit := 50
	if v, ok := a["limit"]; ok {
		if n, valid := numeric(v); valid && n > 0 && n <= maxChangeCommits {
			limit = n
		}
	}
	result := map[string]any{"head": head, "since": since, "commits": []any{}}
	if head == "" {
		return result, nil
	}
	args := []string{"log", "--no-merges", "--format=%x1e%H%x1f%an%x1f%aI%x1f%s", "--name-status", "--no-renames", "-n", strconv.Itoa(limit + 1)}
	if since != "" {
		if !commitID.MatchString(since) {
			return nil, badArg("since must be a commit id from an earlier changes call.")
		}
		if _, err := s.gitInLive(ctx, nil, "merge-base", "--is-ancestor", since, head); err != nil {
			return nil, badArg("since is not an earlier Brain commit; list changes without since to start again.")
		}
		args = append(args, since+".."+head)
	} else {
		args = append(args, head)
	}
	if folder := strings.Trim(stringArg(a, "folder_path"), "/"); folder != "" {
		args = append(args, "--", folder)
	}
	out, err := s.gitInLive(ctx, nil, args...)
	if err != nil {
		return nil, kbErr("UNAVAILABLE", "Brain's history could not be read.")
	}
	commits := []any{}
	records := strings.Split(out, "\x1e")
	for _, record := range records {
		record = strings.TrimSpace(record)
		if record == "" {
			continue
		}
		lines := strings.Split(record, "\n")
		fields := strings.SplitN(lines[0], "\x1f", 4)
		if len(fields) != 4 {
			continue
		}
		files := []any{}
		for _, line := range lines[1:] {
			status, file, ok := strings.Cut(strings.TrimSpace(line), "\t")
			if !ok || strings.HasSuffix(file, ".kb-registry.json") || !s.readableNotePath(p, file) {
				continue
			}
			files = append(files, map[string]any{"path": file, "change": map[string]string{"A": "added", "M": "updated", "D": "removed"}[status[:1]]})
		}
		if len(files) == 0 {
			continue // nothing this caller may see
		}
		commits = append(commits, map[string]any{"commit": fields[0], "author": fields[1], "at": fields[2], "message": fields[3], "files": files})
	}
	if len(commits) > limit {
		commits = commits[:limit]
		result["truncated"] = true
	}
	result["commits"] = commits
	return result, nil
}

func (s *Service) noteDiff(ctx context.Context, p Principal, a map[string]any) (any, error) {
	if !s.hasGitHistory() {
		return nil, kbErr("UNAVAILABLE", "Brain's history is not set up on this server yet.")
	}
	notePath := strings.Trim(stringArg(a, "path"), "/")
	since := strings.TrimSpace(stringArg(a, "since"))
	if err := validatePath(notePath, true); err != nil || !commitID.MatchString(since) {
		return nil, badArg("path (a note) and since (a commit id) are required.")
	}
	if !s.readableNotePath(p, notePath) {
		return nil, kbErr("NOT_FOUND", "Resource not found.")
	}
	head := s.headCommit(ctx)
	if _, err := s.gitInLive(ctx, nil, "merge-base", "--is-ancestor", since, head); err != nil {
		return nil, badArg("since is not an earlier Brain commit.")
	}
	out, err := s.gitInLive(ctx, nil, "diff", "--no-color", "--no-ext-diff", since, head, "--", notePath)
	if err != nil {
		return nil, kbErr("UNAVAILABLE", "Brain's history could not be read.")
	}
	truncated := false
	if len(out) > maxDiffBytes {
		out, truncated = out[:maxDiffBytes], true
	}
	return map[string]any{"path": notePath, "since": since, "head": head, "diff": out, "truncated": truncated}, nil
}
