package server

// A chat's own branch (#2922): the agent asks, the server makes a Git worktree
// for that chat inside the repository (<repo>/.worktrees/<name>, branch
// chat/<name>) and records it against the chat. The chat's working folder does
// not change, so the provider process is not restarted; the agent works in the
// returned folder. Removing the chat removes its worktrees when nothing in them
// would be lost.
import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/safegit"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

const chatBranchFolder = ".worktrees"

type chatBranch struct {
	Repo    string    `json:"repo"`   // repository folder, relative to the Crew's files
	Path    string    `json:"path"`   // worktree folder, relative to the Crew's files
	Branch  string    `json:"branch"` // chat/<name>
	Base    string    `json:"base"`   // commit the branch started from
	Created time.Time `json:"created_at"`
	// A fork's copy of its parent's branch, not yet announced to its agent.
	ForkedFrom string `json:"forked_from,omitempty"`
	Announce   bool   `json:"announce,omitempty"`
}

var chatBranchMu sync.Mutex

func chatBranchStorePath(roots crewroot.Roots) string {
	return filepath.Join(roots.Private, "chat-branches.json")
}

func readChatBranches(roots crewroot.Roots) map[string][]chatBranch {
	all := map[string][]chatBranch{}
	if raw, err := os.ReadFile(chatBranchStorePath(roots)); err == nil {
		_ = json.Unmarshal(raw, &all)
	}
	return all
}

func writeChatBranches(roots crewroot.Roots, all map[string][]chatBranch) error {
	for key, list := range all {
		if len(list) == 0 {
			delete(all, key)
		}
	}
	raw, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(roots.Private, 0o700); err != nil {
		return err
	}
	return writeFileAtomic(chatBranchStorePath(roots), raw)
}

// chatBranchesFor lists the chat's branches whose folders still exist.
func chatBranchesFor(roots crewroot.Roots, chatKey string) []chatBranch {
	if chatKey == "" || roots.Files == "" || roots.Private == "" {
		return nil
	}
	chatBranchMu.Lock()
	defer chatBranchMu.Unlock()
	var live []chatBranch
	for _, b := range readChatBranches(roots)[chatKey] {
		if _, err := os.Stat(filepath.Join(roots.Files, filepath.FromSlash(b.Path))); err == nil {
			live = append(live, b)
		}
	}
	return live
}

// chatBranchCheckouts lists the chat's own worktree folders (absolute), for the
// reply file card.
func chatBranchCheckouts(roots crewroot.Roots, chatKey string) []string {
	if chatKey == "" || roots.Files == "" || roots.Private == "" {
		return nil
	}
	chatBranchMu.Lock()
	defer chatBranchMu.Unlock()
	var dirs []string
	for _, b := range readChatBranches(roots)[chatKey] {
		dirs = append(dirs, filepath.Join(roots.Files, filepath.FromSlash(b.Path)))
	}
	return dirs
}

func chatBranchGit(ctx context.Context, root, dir string, args ...string) (string, error) {
	out, err := safegit.CommandIn(ctx, root, dir, nil, args...).CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil && text != "" {
		err = errors.New(text)
	}
	return text, err
}

// startChatBranch returns the chat's branch for repo, creating it when needed.
func startChatBranch(ctx context.Context, roots crewroot.Roots, chatKey, repo, purpose string) (chatBranch, bool, error) {
	if roots.Files == "" || chatKey == "" {
		return chatBranch{}, false, errors.New("own branches are available only in a Crew chat")
	}
	root, err := filepath.EvalSymlinks(roots.Files)
	if err != nil {
		return chatBranch{}, false, err
	}
	repo = strings.TrimSpace(repo)
	if filepath.IsAbs(repo) {
		real, err := filepath.EvalSymlinks(repo)
		if err != nil {
			return chatBranch{}, false, fmt.Errorf("no folder %q in this Crew", repo)
		}
		if repo, err = filepath.Rel(root, real); err != nil {
			return chatBranch{}, false, err
		}
	}
	rel, ok := workspaceGitRelFile(strings.Trim(filepath.ToSlash(repo), "/"))
	if repo == "." || repo == "" {
		rel, ok = "", true
	}
	if !ok {
		return chatBranch{}, false, fmt.Errorf("give the repository folder inside this Crew, for example code/app")
	}
	dir, status, message := workspaceGitResolveRepo(root, rel)
	if status != 0 {
		return chatBranch{}, false, fmt.Errorf("%s is not a Git repository in this Crew (%s)", firstNonEmptyTrimmed(rel, "the Crew folder"), message)
	}
	if err := crewroot.CheckSyncWrite(fsutil.WorkspaceDocsRoot(), dir); err != nil {
		return chatBranch{}, false, err
	}
	gitDir, common, _ := safegit.CheckoutInside(root, dir)
	if filepath.Dir(gitDir) == filepath.Join(common, "worktrees") {
		return chatBranch{}, false, errors.New("this folder is already a branch folder; give its main repository folder")
	}
	repoRel := filepath.ToSlash(mustRel(root, dir))

	chatBranchMu.Lock()
	defer chatBranchMu.Unlock()
	all := readChatBranches(roots)
	var live []chatBranch
	for _, b := range all[chatKey] {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(b.Path))); err != nil {
			continue // removed by hand; forget it
		}
		if b.Repo == repoRel {
			return b, true, nil
		}
		live = append(live, b)
	}
	all[chatKey] = live
	unlock := workspaceGitLock(dir)
	defer unlock()
	for _, marker := range []string{"MERGE_HEAD", "rebase-merge", "rebase-apply", "CHERRY_PICK_HEAD"} {
		if _, err := os.Lstat(filepath.Join(gitDir, marker)); err == nil {
			return chatBranch{}, false, errors.New("this repository is in the middle of a merge or rebase; finish it first, or edit it in place")
		}
	}
	// Start from what the repository's current branch tracks (for example
	// origin/dev), else its current commit.
	base := "HEAD"
	if upstream, err := chatBranchGit(ctx, root, dir, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}"); err == nil && upstream != "" {
		base = upstream
	}
	baseCommit, err := chatBranchGit(ctx, root, dir, "rev-parse", "--verify", "--quiet", base+"^{commit}")
	if err != nil || baseCommit == "" {
		return chatBranch{}, false, errors.New("this repository has no commits yet; make a first commit, or edit it in place")
	}
	name := slugifyCrewTitle(purpose)
	if strings.TrimSpace(purpose) == "" {
		name = "work"
	}
	if len(name) > 40 {
		name = strings.Trim(name[:40], "-")
	}
	branch, target, err := addChatWorktree(ctx, root, dir, common, name, baseCommit)
	if err != nil {
		return chatBranch{}, false, err
	}
	b := chatBranch{Repo: repoRel, Path: filepath.ToSlash(mustRel(root, target)), Branch: branch, Base: baseCommit, Created: time.Now().UTC()}
	all[chatKey] = append(all[chatKey], b)
	if err := writeChatBranches(roots, all); err != nil {
		return b, false, err
	}
	log.Printf("[CHAT_BRANCH] event=created chat=%s repo=%s branch=%s base=%s", chatKey, repoRel, branch, shortCommit(baseCommit))
	return b, false, nil
}

// addChatWorktree creates <dir>/.worktrees/<name> on a new branch chat/<name>
// (numbered when taken) at commit. The caller holds the repository's lock.
func addChatWorktree(ctx context.Context, root, dir, common, name, commit string) (string, string, error) {
	candidate := name
	for i := 2; ; i++ {
		_, branchErr := chatBranchGit(ctx, root, dir, "rev-parse", "--verify", "--quiet", "refs/heads/chat/"+candidate)
		_, folderErr := os.Lstat(filepath.Join(dir, chatBranchFolder, candidate))
		if branchErr != nil && os.IsNotExist(folderErr) {
			break
		}
		if i > 50 {
			return "", "", errors.New("too many chat branches with this name; give a different purpose")
		}
		candidate = fmt.Sprintf("%s-%d", name, i)
	}
	branch := "chat/" + candidate
	if !workspaceGitBranchName(ctx, dir, branch) {
		return "", "", fmt.Errorf("cannot use %q as a branch name", branch)
	}
	// Keep the branch folders out of the repository's own status.
	if err := chatBranchExclude(common); err != nil {
		return "", "", err
	}
	target := filepath.Join(dir, chatBranchFolder, candidate)
	if _, err := chatBranchGit(ctx, root, dir, "worktree", "add", "--no-track", "-b", branch, target, commit); err != nil {
		return "", "", fmt.Errorf("could not create the branch folder: %v", err)
	}
	return branch, target, nil
}

func chatBranchExclude(common string) error {
	path := filepath.Join(common, "info", "exclude")
	raw, _ := os.ReadFile(path)
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == "/"+chatBranchFolder+"/" {
			return nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	text := string(raw)
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	return os.WriteFile(path, []byte(text+"/"+chatBranchFolder+"/\n"), 0o644)
}

// removeChatBranches runs when a chat is removed. A worktree is removed only
// when nothing would be lost: no uncommitted edits, and its branch adds no
// commits or every commit is on a remote. Others stay, recorded under no chat.
func removeChatBranches(ctx context.Context, roots crewroot.Roots, chatKey string) {
	if chatKey == "" || roots.Files == "" || roots.Private == "" {
		return
	}
	root, err := filepath.EvalSymlinks(roots.Files)
	if err != nil {
		return
	}
	chatBranchMu.Lock()
	defer chatBranchMu.Unlock()
	all := readChatBranches(roots)
	list := all[chatKey]
	if len(list) == 0 {
		return
	}
	delete(all, chatKey)
	var kept []chatBranch
	for _, b := range list {
		reason := removeChatBranch(ctx, root, b)
		if reason != "" {
			kept = append(kept, b)
		}
		log.Printf("[CHAT_BRANCH] event=chat_removed chat=%s repo=%s branch=%s kept=%t reason=%s", chatKey, b.Repo, b.Branch, reason != "", firstNonEmptyTrimmed(reason, "removed"))
	}
	if len(kept) > 0 {
		all["kept:"+chatKey] = kept
	}
	if err := writeChatBranches(roots, all); err != nil {
		log.Printf("[CHAT_BRANCH] event=record_failed chat=%s err=%v", chatKey, err)
	}
}

func removeChatBranch(ctx context.Context, root string, b chatBranch) string {
	repo := filepath.Join(root, filepath.FromSlash(b.Repo))
	tree := filepath.Join(root, filepath.FromSlash(b.Path))
	if _, err := os.Stat(tree); os.IsNotExist(err) {
		_, _ = chatBranchGit(ctx, root, repo, "worktree", "prune")
		return ""
	}
	if status, err := chatBranchGit(ctx, root, tree, "status", "--porcelain", "--untracked-files=normal"); err != nil {
		return "unreadable"
	} else if status != "" {
		return "uncommitted_edits"
	}
	tip, err := chatBranchGit(ctx, root, tree, "rev-parse", "HEAD")
	if err != nil {
		return "unreadable"
	}
	if tip != b.Base {
		if remotes, err := chatBranchGit(ctx, root, tree, "branch", "-r", "--contains", tip); err != nil || remotes == "" {
			return "unpushed_commits"
		}
	}
	unlock := workspaceGitLock(repo)
	defer unlock()
	if _, err := chatBranchGit(ctx, root, repo, "worktree", "remove", tree); err != nil {
		return "remove_failed"
	}
	_, _ = chatBranchGit(ctx, root, repo, "branch", "-D", b.Branch)
	return ""
}

func mustRel(base, target string) string {
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return target
	}
	return rel
}

func shortCommit(commit string) string {
	if len(commit) > 12 {
		return commit[:12]
	}
	return commit
}

// forkChatBranches gives a forked chat its own copy of each of the parent's
// branch folders: a new branch chat/<name>-fork from the parent's current
// commit, with the parent's uncommitted edits (tracked and untracked) copied
// in. The parent's folders are left as they are. It returns the new branches.
func forkChatBranches(ctx context.Context, roots crewroot.Roots, sourceKey, forkKey string) []chatBranch {
	if sourceKey == "" || forkKey == "" || roots.Files == "" || roots.Private == "" {
		return nil
	}
	root, err := filepath.EvalSymlinks(roots.Files)
	if err != nil {
		return nil
	}
	chatBranchMu.Lock()
	defer chatBranchMu.Unlock()
	all := readChatBranches(roots)
	var made []chatBranch
	for _, parent := range all[sourceKey] {
		b, err := forkChatBranch(ctx, root, parent)
		if err != nil {
			log.Printf("[CHAT_BRANCH] event=fork_failed chat=%s fork=%s repo=%s err=%v", sourceKey, forkKey, parent.Repo, err)
			continue
		}
		made = append(made, b)
		log.Printf("[CHAT_BRANCH] event=forked chat=%s fork=%s repo=%s from=%s branch=%s base=%s", sourceKey, forkKey, b.Repo, parent.Branch, b.Branch, shortCommit(b.Base))
	}
	if len(made) == 0 {
		return nil
	}
	all[forkKey] = append(all[forkKey], made...)
	if err := writeChatBranches(roots, all); err != nil {
		log.Printf("[CHAT_BRANCH] event=record_failed chat=%s err=%v", forkKey, err)
		return nil
	}
	return made
}

func forkChatBranch(ctx context.Context, root string, parent chatBranch) (chatBranch, error) {
	repo := filepath.Join(root, filepath.FromSlash(parent.Repo))
	source := filepath.Join(root, filepath.FromSlash(parent.Path))
	if _, err := os.Stat(source); err != nil {
		return chatBranch{}, errors.New("the parent's branch folder is gone")
	}
	tip, err := chatBranchGit(ctx, root, source, "rev-parse", "HEAD")
	if err != nil {
		return chatBranch{}, err
	}
	// The parent's uncommitted tracked edits as a commit object; nothing in
	// the parent folder or the stash list changes.
	edits, err := chatBranchGit(ctx, root, source, "stash", "create")
	if err != nil {
		return chatBranch{}, err
	}
	untracked, err := chatBranchGit(ctx, root, source, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return chatBranch{}, err
	}
	_, common, _ := safegit.CheckoutInside(root, repo)
	unlock := workspaceGitLock(repo)
	defer unlock()
	branch, target, err := addChatWorktree(ctx, root, repo, common, strings.TrimPrefix(parent.Branch, "chat/")+"-fork", tip)
	if err != nil {
		return chatBranch{}, err
	}
	if edits != "" {
		if _, err := chatBranchGit(ctx, root, target, "stash", "apply", edits); err != nil {
			log.Printf("[CHAT_BRANCH] event=fork_edits_not_copied branch=%s err=%v", branch, err)
		}
	}
	for _, name := range strings.Split(untracked, "\x00") {
		if name == "" {
			continue
		}
		if err := copyChatBranchFile(filepath.Join(source, filepath.FromSlash(name)), filepath.Join(target, filepath.FromSlash(name))); err != nil {
			log.Printf("[CHAT_BRANCH] event=fork_file_not_copied branch=%s err=%v", branch, err)
		}
	}
	return chatBranch{Repo: parent.Repo, Path: filepath.ToSlash(mustRel(root, target)), Branch: branch, Base: tip, Created: time.Now().UTC(), ForkedFrom: parent.Branch, Announce: true}, nil
}

func copyChatBranchFile(from, to string) error {
	info, err := os.Lstat(from)
	if err != nil || !info.Mode().IsRegular() {
		return err // links and special files are not copied
	}
	data, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	return os.WriteFile(to, data, info.Mode().Perm())
}

// takeChatBranchAnnouncement returns, once, the line that tells a forked
// chat's agent which branch folders are its own.
func takeChatBranchAnnouncement(roots crewroot.Roots, chatKey string) string {
	if chatKey == "" || roots.Files == "" || roots.Private == "" {
		return ""
	}
	chatBranchMu.Lock()
	defer chatBranchMu.Unlock()
	all := readChatBranches(roots)
	var lines []string
	for i, b := range all[chatKey] {
		if !b.Announce {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s is on branch %s in %s (copied from %s)", b.Repo, b.Branch, filepath.Join(roots.Files, filepath.FromSlash(b.Path)), b.ForkedFrom))
		all[chatKey][i].Announce = false
	}
	if len(lines) == 0 {
		return ""
	}
	if err := writeChatBranches(roots, all); err != nil {
		return ""
	}
	return "[This chat is a fork with its own branch folders: " + strings.Join(lines, "; ") + ". Work only in these folders, not in the parent chat's.]"
}
