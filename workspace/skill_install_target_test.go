package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
)

func TestSkillInstallTargetDir(t *testing.T) {
	docs := t.TempDir()
	if got, err := skillInstallTargetDir(docs, ""); err != nil || got != filepath.Join(docs, "skills") {
		t.Fatalf("library target = %q %v", got, err)
	}
	ok := "_users/u1/Chats/Code/projects/app-1234abcd/skills"
	if err := os.MkdirAll(filepath.Join(docs, filepath.Dir(ok)), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := skillInstallTargetDir(docs, ok); err != nil || got != filepath.Join(docs, ok) {
		t.Fatalf("project target = %q %v", got, err)
	}
	for _, bad := range []string{
		"skills", "_users/u1/Chats/Code/projects/app/code", "_users/u1/Chats/Code/projects/../../x/skills",
		"Workflow/w/skills", "_users/u1/Chats/Code/projects/app/skills/extra", "/etc/skills",
		"_users/u1/Chats/Code/projects/missing-project/skills",
	} {
		if _, err := skillInstallTargetDir(docs, bad); err == nil {
			t.Fatalf("accepted target %q", bad)
		}
	}
}

// An agent can plant links inside its own project; an install or delete must
// never follow one into another user's tree.
func TestProjectSkillsDirRefusesLinks(t *testing.T) {
	docs := t.TempDir()
	victim := filepath.Join(docs, "_users/alice/Chats/Code/projects/a-1/skills")
	if err := os.MkdirAll(victim, 0o755); err != nil {
		t.Fatal(err)
	}
	mine := filepath.Join(docs, "_users/bob/Chats/Code/projects/b-1")
	if err := os.MkdirAll(mine, 0o755); err != nil {
		t.Fatal(err)
	}
	// skills/ itself is a link to alice's folder.
	if err := os.Symlink(victim, filepath.Join(mine, "skills")); err != nil {
		t.Fatal(err)
	}
	if _, err := projectSkillsDir(docs, "_users/bob/Chats/Code/projects/b-1/skills"); err == nil {
		t.Fatal("a linked skills folder was accepted")
	}
	// A linked project folder.
	if err := os.Symlink(filepath.Dir(victim), filepath.Join(docs, "_users/bob/Chats/Code/projects/b-2")); err != nil {
		t.Fatal(err)
	}
	if _, err := projectSkillsDir(docs, "_users/bob/Chats/Code/projects/b-2/skills"); err == nil {
		t.Fatal("a linked project folder was accepted")
	}

	// A link planted where a skill is installed is refused, not written through.
	real := filepath.Join(docs, "_users/bob/Chats/Code/projects/b-3")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	dir, err := projectSkillsDir(docs, "_users/bob/Chats/Code/projects/b-3/skills")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(dir, "demo")); err != nil {
		t.Fatal(err)
	}
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := installSkillFolder(src, dir, "demo"); err == nil {
		t.Fatal("installed through a planted link")
	}
	if _, err := os.Stat(filepath.Join(victim, "SKILL.md")); !os.IsNotExist(err) {
		t.Fatal("the install wrote into another user's folder")
	}
	// A real target is installed.
	if err := os.Remove(filepath.Join(dir, "demo")); err != nil {
		t.Fatal(err)
	}
	if err := installSkillFolder(src, dir, "demo"); err != nil {
		t.Fatalf("install: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "demo", "SKILL.md")); err != nil {
		t.Fatalf("installed skill missing: %v", err)
	}
}

func TestProjectSkillDeleteNeverFollowsLinks(t *testing.T) {
	gin.SetMode(gin.TestMode)
	docs := t.TempDir()
	viper.Set("docs-dir", docs)
	t.Cleanup(func() { viper.Set("docs-dir", "") })
	victim := filepath.Join(docs, "_users/alice/Chats/Code/projects/a-1/skills/demo")
	if err := os.MkdirAll(victim, 0o755); err != nil {
		t.Fatal(err)
	}
	skills := filepath.Join(docs, "_users/bob/Chats/Code/projects/b-1/skills")
	if err := os.MkdirAll(skills, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(skills, "demo")); err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.POST("/delete", handleProjectSkillDelete)
	call := func(body string) int {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/delete", strings.NewReader(body)))
		return rec.Code
	}
	if code := call(`{"target_dir":"_users/bob/Chats/Code/projects/b-1/skills","name":"demo"}`); code != http.StatusOK {
		t.Fatalf("delete = %d", code)
	}
	if _, err := os.Stat(victim); err != nil {
		t.Fatal("deleting a linked skill removed the other user's folder")
	}
	if _, err := os.Lstat(filepath.Join(skills, "demo")); !os.IsNotExist(err) {
		t.Fatal("the link itself was not removed")
	}
	for _, body := range []string{
		`{"target_dir":"_users/bob/Chats/Code/projects/b-1/skills","name":"../x"}`,
		`{"target_dir":"_users/alice/Chats/Code/projects/../a-1/skills","name":"demo"}`,
	} {
		if code := call(body); code != http.StatusBadRequest {
			t.Fatalf("%s = %d", body, code)
		}
	}
}
