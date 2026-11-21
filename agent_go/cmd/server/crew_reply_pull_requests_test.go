package server

import "testing"

// Several chats can share one GitHub login, so a turn links only the pull
// requests its own tool calls name (PLAT-746). These cases pin that rule.
func TestReplyTurnNamesOnlyItsOwnPullRequests(t *testing.T) {
	ghCreate := "{\"command\":\"gh pr create --base dev --fill\"}\nhttps://github.com/example/app/pull/850\n"
	gitPush := "{\"command\":\"git push origin head:refs/heads/agent/topic\"}\nto github.com:example/app.git\n * [new branch]      head -\\u003e agent/topic\n"
	cases := []struct {
		name   string
		texts  []string
		number int
		branch string
		want   bool
	}{
		{"gh pr create prints the link", []string{ghCreate}, 850, "other", true},
		{"a longer PR number is another PR", []string{ghCreate}, 85, "other", false},
		{"push output names repo and branch", []string{gitPush}, 12, "agent/topic", true},
		{"a branch that only starts the same", []string{gitPush}, 12, "agent/top", false},
		{"branch without its repository", []string{"git checkout agent/topic"}, 12, "agent/topic", false},
		{"a fetch listing teammates' branches is not this turn's PR", []string{"{\"command\":\"git pull --ff-only\"}\nfrom github.com:example/app\n   a1b2..c3d4  agent/topic -> origin/agent/topic\n"}, 12, "agent/topic", false},
		{"main is never matched by name", []string{"to github.com:example/app.git\n main -> main"}, 12, "main", false},
	}
	for _, tc := range cases {
		if got := replyTurnNamesPR(tc.texts, "example/app", tc.number, tc.branch); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
	if repos := replyTurnRepos([]string{gitPush, ghCreate}); len(repos) != 1 || repos[0] != "example/app" {
		t.Errorf("repos = %v, want [example/app]", repos)
	}
}
