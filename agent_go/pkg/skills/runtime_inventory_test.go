package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimeInventoryReportsPhysicalFilesWithoutNameFilters(t *testing.T) {
	dir := t.TempDir()
	write := func(file, content string) {
		t.Helper()
		target := filepath.Join(dir, file)
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(".future-provider/skills/unexpected/SKILL.md", "---\nname: actual-runtime-name\ndescription: Visible regardless of registry\n---\nCurrent body")
	write(".future-provider/skills/unexpected/references/guide.md", "Live supporting file")
	write(".agents/skills/unexpected/SKILL.md", "malformed but visible")
	write(".agents/skills/unexpected/.hidden-reference", "Hidden-name supporting file")
	write(".sandbox-cache/home/.agents/skills/not-a-projection/SKILL.md", "Do not confuse CLI home with attached files")
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "private"), []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "project")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "private"), filepath.Join(dir, ".agents/skills/unexpected/escape")); err != nil {
		t.Fatal(err)
	}
	got, err := ReadRuntimeInventory(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Skills) != 2 {
		t.Fatalf("lost physical duplicates or invented skills: %+v", got)
	}
	if got.Skills[0].ValidationError == "" || got.Skills[0].Content != "malformed but visible" {
		t.Fatal("malformed file hidden")
	}
	if len(got.Skills[0].Files) != 3 {
		t.Fatalf("missing supporting files: %+v", got.Skills[0].Files)
	}
	if body, err := ReadRuntimeSkillFile(dir, ".future-provider/skills/unexpected/references/guide.md", got); err != nil || body != "Live supporting file" {
		t.Fatalf("guide: %q %v", body, err)
	}
	if _, err := ReadRuntimeSkillFile(dir, ".agents/skills/unexpected/escape", got); err == nil {
		t.Fatal("symlink escaped runtime")
	}
	if _, err := ReadRuntimeSkillFile(dir, "../../private", got); err == nil {
		t.Fatal("path traversal accepted")
	}
	write(".future-provider/skills/unexpected/references/guide.md", "Changed on disk")
	if body, err := ReadRuntimeSkillFile(dir, ".future-provider/skills/unexpected/references/guide.md", got); err != nil || body != "Changed on disk" {
		t.Fatal("read an old snapshot")
	}
	if strings.Contains(got.Skills[1].Content, "secret") {
		t.Fatal("private content leaked")
	}
}
