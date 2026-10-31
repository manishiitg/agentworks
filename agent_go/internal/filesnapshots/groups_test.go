package filesnapshots

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/commands"
)

// A checkout the reply cloned is one group with its branch, labelled new, and
// lists the reply's saved changes (its edits since the cloned commit, see
// TestNewCheckoutComparesAgainstItsCheckedOutCommit); non-Git files stay apart.
func TestNewCheckoutGroupAndPlainFilesApart(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "code", "app")
	if err := os.MkdirAll(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo, "-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	run("init", "--quiet", "-b", "feature-x")
	for i := 0; i < 30; i++ {
		if err := os.WriteFile(filepath.Join(repo, fmt.Sprintf("f%02d.txt", i)), []byte("one\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	run("add", ".")
	run("commit", "--quiet", "-m", "init")
	run("remote", "add", "origin", "https://user:secret@github.com/acme/app.git")
	changes := []Change{{Path: "code/app/f03.txt", Status: "M"}, {Path: "code/app/new.txt", Status: "A"}, {Path: "notes.md", Status: "M"}}

	groups := GroupChanges(context.Background(), root, changes, []string{"code/app"}, func(string) bool { return true })
	if len(groups) != 2 || groups[0].Kind != "repo" || groups[1].Kind != "crew_files" {
		t.Fatalf("groups = %+v", groups)
	}
	g := groups[0]
	if g.Root != "code/app" || g.Branch != "feature-x" || !g.NewCheckout || g.GitHubRepo != "acme/app" {
		t.Fatalf("repo group = %+v", g)
	}
	if len(g.Files) != 2 || g.Files[0].Path != "code/app/f03.txt" || g.Files[1].Path != "code/app/new.txt" {
		t.Fatalf("files = %+v", g.Files)
	}
	if len(groups[1].Files) != 1 || groups[1].Files[0].Path != "notes.md" {
		t.Fatalf("crew files = %+v", groups[1])
	}
}

// PLAT-746: an existing checkout with older uncommitted edits lists only the
// files this reply changed, so the card's count matches its rows.
func TestExistingCheckoutListsOnlyThisReplysFiles(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "app")
	os.MkdirAll(repo, 0o700)
	git := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", repo, "-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	git("init", "--quiet")
	for _, name := range []string{"a.txt", "b.txt"} {
		os.WriteFile(filepath.Join(repo, name), []byte("one\n"), 0o600)
	}
	git("add", ".")
	git("commit", "--quiet", "-m", "init")
	os.WriteFile(filepath.Join(repo, "a.txt"), []byte("older edit\n"), 0o600)
	os.WriteFile(filepath.Join(repo, "b.txt"), []byte("one\ntwo\n"), 0o600)
	groups := GroupChanges(context.Background(), root, []Change{{Path: "app/b.txt", Status: "M"}}, nil, func(string) bool { return true })
	if len(groups) != 1 || len(groups[0].Files) != 1 || groups[0].Files[0].Path != "app/b.txt" {
		t.Fatalf("groups = %+v", groups)
	}
}

func TestGitHubRepoDropsCredentialsAndOtherHosts(t *testing.T) {
	for remote, want := range map[string]string{
		"git@github.com:acme/app.git":               "acme/app",
		"https://tok@github.com/acme/app":           "acme/app",
		"ssh://git@github.com/acme/app.git":         "acme/app",
		"https://gitlab.example.com/acme/app.git":   "",
		"https://github.com/acme/app?token=example": "",
		"https://github.com/acme/app#example":       "",
	} {
		if got := GitHubRepo(remote); got != want {
			t.Errorf("GitHubRepo(%q) = %q, want %q", remote, got, want)
		}
	}
}

// The card's one Git step comes from the checkout itself: nothing to do once
// everything is pushed, share or update the PR when work is not on GitHub yet,
// and get the latest first when the branch is behind its remote.
func TestNextStepFollowsTheCheckout(t *testing.T) {
	base := t.TempDir()
	git := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	upstream, other, root := filepath.Join(base, "up.git"), filepath.Join(base, "other"), filepath.Join(base, "crew")
	repo := filepath.Join(root, "app")
	git(base, "init", "--quiet", "--bare", "-b", "main", upstream)
	git(base, "clone", "--quiet", upstream, repo)
	git(repo, "checkout", "--quiet", "-b", "feature")
	os.WriteFile(filepath.Join(repo, "a.txt"), []byte("one\n"), 0o600)
	git(repo, "add", ".")
	git(repo, "commit", "--quiet", "-m", "one")
	git(repo, "push", "--quiet", "-u", "origin", "feature")
	git(repo, "remote", "set-url", "origin", "https://github.com/acme/app.git")
	step := func(pr *PullRequest) string {
		groups := GroupChanges(context.Background(), root, []Change{{Path: "app/a.txt", Status: "M"}}, nil, func(string) bool { return true })
		groups[0].PR = pr
		return NextStep(groups[0])
	}
	if got := step(nil); got != "none" {
		t.Fatalf("pushed and clean: %q", got)
	}
	os.WriteFile(filepath.Join(repo, "a.txt"), []byte("one\ntwo\n"), 0o600)
	if got := step(nil); got != "share_pr" {
		t.Fatalf("edited, no PR: %q", got)
	}
	git(repo, "commit", "--quiet", "-am", "two")
	if got := step(&PullRequest{State: "open"}); got != "update_pr" {
		t.Fatalf("unpushed commit, open PR: %q", got)
	}
	git(base, "clone", "--quiet", "-b", "feature", upstream, other)
	os.WriteFile(filepath.Join(other, "b.txt"), []byte("b\n"), 0o600)
	git(other, "add", ".")
	git(other, "commit", "--quiet", "-m", "b")
	git(other, "push", "--quiet", "origin", "feature")
	git(repo, "fetch", "--quiet", upstream, "feature:refs/remotes/origin/feature")
	if got := step(&PullRequest{State: "open"}); got != "get_latest" {
		t.Fatalf("behind its remote: %q", got)
	}
}

// The card's actions are decided here (PLAT-746-158): a Run person gets only
// Review changes; an editor gets Commit, Push, the next Git step, a fix action
// only for what GitHub reports on the open PR, and their card commands.
func TestCardActionsFollowViewerAndPullRequest(t *testing.T) {
	group := Group{Kind: "repo", Root: "app", Name: "app", Branch: "fix-tts", GitHubRepo: "o/r", Files: []Change{{Path: "app/tts.py", Status: "M"}},
		PR: &PullRequest{Number: 305, URL: "https://github.com/o/r/pull/305", State: "open", Checks: "passing", Review: "none"}, NextStep: "update_pr"}
	card := []commands.Command{
		{Frontmatter: commands.CommandFrontmatter{Name: "check-this-change", Description: "Check this change", ShowOnCard: true}, Content: "Look over the files.\n{{context}}"},
		{Frontmatter: commands.CommandFrontmatter{Name: "slash-only"}, Content: "x"},
	}
	placed := func(actions []CardAction) []string {
		var out []string
		for _, action := range actions {
			out = append(out, action.Placement+":"+action.ID)
		}
		return out
	}
	if got := placed(CardActions([]Group{group}, false, card)); !reflect.DeepEqual(got, []string{"row:review"}) {
		t.Fatalf("Run person: %v", got)
	}
	actions := CardActions([]Group{group}, true, card)
	want := []string{"row:review", "row:commit", "row:push", "more:next:app", "row:command:check-this-change", "row:add_button"}
	if got := placed(actions); !reflect.DeepEqual(got, want) {
		t.Fatalf("editor, passing checks: %v", got)
	}
	// Add must identify the platform surface and existing setup path; a vague
	// request previously sent a coding agent into its native add-on authoring.
	for _, context := range []string{"AgentWorks", "Files changed during this reply", "crew-builder", "manage_custom_commands", "show_on_card=true"} {
		if !strings.Contains(actions[len(actions)-1].Prompt, context) {
			t.Fatalf("Add button draft is missing %q", context)
		}
	}
	if !strings.Contains(actions[1].Prompt, "on branch fix-tts. Stage only the files listed here, by name") || !strings.Contains(actions[2].Prompt, "Never push the default branch") || !strings.Contains(actions[2].Prompt, "\n- app/tts.py\n") {
		t.Fatalf("commit and push prompts: %q / %q", actions[1].Prompt, actions[2].Prompt)
	}
	if actions[3].Label != "Update the pull request" || !strings.HasPrefix(actions[3].Prompt, "Update PR #305 in o/r with this reply's work on branch fix-tts.") {
		t.Fatalf("next step: %+v", actions[3])
	}
	if got := actions[4].Prompt; got != "Look over the files.\nContext: this reply changed these files: app/tts.py in app on branch fix-tts; PR https://github.com/o/r/pull/305" {
		t.Fatalf("card command prompt: %q", got)
	}
	group.PR.Checks, group.PR.FailingChecks = "failing", []string{"build", "lint"}
	actions = CardActions([]Group{group}, true, nil)
	want = []string{"row:review", "row:commit", "row:push", "more:next:app", "more:fix-checks:app", "row:add_button", "pr_line:fix-checks:app:link"}
	if got := placed(actions); !reflect.DeepEqual(got, want) {
		t.Fatalf("editor, failing checks: %v", got)
	}
	if got := actions[4].Prompt; got != "PR #305 in o/r has failing checks: build, lint. Open the failing runs, fix the cause on branch fix-tts, push, and tell me what you changed." {
		t.Fatalf("fix prompt: %q", got)
	}
}

// A chat's "work done" is the commits after the commit the chat first saw;
// what is not committed is listed separately, and work committed before the
// chat started never shows (PLAT-746: merged work kept counting as changes).
func TestChatGitStateSplitsCommitsFromUncommitted(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "app")
	os.MkdirAll(repo, 0o700)
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", repo, "-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "--quiet")
	os.WriteFile(filepath.Join(repo, "old.txt"), []byte("before the chat\n"), 0o600)
	git("add", ".")
	git("commit", "--quiet", "-m", "before the chat")
	start := git("rev-parse", "HEAD")
	os.WriteFile(filepath.Join(repo, "fix.txt"), []byte("fix\n"), 0o600)
	git("add", ".")
	git("commit", "--quiet", "-m", "Fix in this chat")
	os.WriteFile(filepath.Join(repo, "draft.txt"), []byte("not yet\n"), 0o600)
	g := Group{Kind: "repo", Root: "app"}
	AddChatGitState(context.Background(), root, &g, start, func(string) bool { return true })
	if !g.CommitsKnown || len(g.Commits) != 1 || g.Commits[0].Subject != "Fix in this chat" || g.Commits[0].Files != 1 {
		t.Fatalf("commits = %+v known=%v", g.Commits, g.CommitsKnown)
	}
	if len(g.Uncommitted) != 1 || g.Uncommitted[0].Path != "app/draft.txt" {
		t.Fatalf("uncommitted = %+v", g.Uncommitted)
	}
	for _, bad := range []string{"", "--output=/tmp/x", "HEAD~1"} {
		unknown := Group{Kind: "repo", Root: "app"}
		AddChatGitState(context.Background(), root, &unknown, bad, func(string) bool { return true })
		if unknown.CommitsKnown || len(unknown.Commits) != 0 {
			t.Fatalf("start %q must list no commits: %+v", bad, unknown)
		}
	}
}
