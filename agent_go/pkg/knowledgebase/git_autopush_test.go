package knowledgebase

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Owner, 2026-10-08: Brain's saves must reach its Git backup without anyone asking, and an automatic push must
// never overwrite a remote that was edited elsewhere.
func TestAutoPushDeliversCommitsAndNeverOverwritesTheRemote(t *testing.T) {
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	gitTest(t, "init", "-q", "--bare", "-b", "main", remote)
	s, err := New(Config{Root: filepath.Join(root, "data"), LiveRoot: filepath.Join(root, "docs", "Brain"), OrganizationID: "org", BackupRemote: remote, BackupBranch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.EnsureGitRepository(context.Background()); err != nil {
		t.Fatal(err)
	}
	commit := func(name, author string) string {
		if err := os.WriteFile(filepath.Join(s.live, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
		gitTest(t, "-C", s.live, "add", name)
		gitTest(t, "-C", s.live, "-c", "user.name="+author, "-c", "user.email="+author+"@example.com", "commit", "-q", "-m", "Add "+name)
		return gitTest(t, "-C", s.live, "rev-parse", "HEAD")
	}
	head := commit("a.md", "yoav")
	if pushed, err := s.AutoPush(context.Background(), time.Hour, time.Now()); err != nil || pushed {
		t.Fatalf("pushed during the quiet period: %v %v", pushed, err)
	}
	if pushed, err := s.AutoPush(context.Background(), 0, time.Now().Add(time.Minute)); err != nil || !pushed {
		t.Fatalf("not pushed: %v %v", pushed, err)
	}
	if tip := gitTest(t, "--git-dir="+remote, "rev-parse", "main"); tip != head {
		t.Fatalf("remote main = %s, want %s", tip, head)
	}
	if author := gitTest(t, "--git-dir="+remote, "log", "-1", "--format=%an", "main"); author != "yoav" {
		t.Fatalf("remote commit author = %q, want the person who saved", author)
	}
	// Someone edits the remote directly; Brain keeps saving. The automatic push must stop, not force.
	other := filepath.Join(root, "other")
	gitTest(t, "clone", "-q", remote, other)
	if err := os.WriteFile(filepath.Join(other, "elsewhere.md"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitTest(t, "-C", other, "add", "elsewhere.md")
	gitTest(t, "-C", other, "-c", "user.name=someone", "-c", "user.email=s@example.com", "commit", "-q", "-m", "elsewhere")
	gitTest(t, "-C", other, "push", "-q", "origin", "HEAD:main")
	elsewhere := gitTest(t, "-C", other, "rev-parse", "HEAD")
	commit("b.md", "tom")
	_, err = s.AutoPush(context.Background(), 0, time.Now().Add(2*time.Minute))
	if err == nil || !strings.Contains(err.Error(), "has commits Brain's folder does not") {
		t.Fatalf("expected a refusal, got %v", err)
	}
	if tip := gitTest(t, "--git-dir="+remote, "rev-parse", "main"); tip != elsewhere {
		t.Fatal("the automatic push overwrote a remote edited elsewhere")
	}
	if st := s.LastAutoPush(); st == nil || st.OK || st.Error == "" {
		t.Fatalf("the refusal was not recorded: %+v", st)
	}
}
