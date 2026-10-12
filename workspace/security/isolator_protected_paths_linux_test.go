//go:build linux

package security

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// 856-59: a shared caller's turn on a Crew writes the project's working files but never the files that define the Crew.
// The server passes them as BlockedWritePaths inside a writable project root. From a real shell in the sandbox, writing
// them, deleting them, moving them away, replacing them with a link, writing through a link, or moving the project folder
// away must all fail, while an ordinary working file is written. Skips (not fails) when no sandbox backend is available
// in this environment, like the neighbouring tests.
func TestProtectedCrewPathsSurviveShellWriteDeleteMoveAndSymlink(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "crew")
	memory := filepath.Join(root, "MEMORY.md")
	skills := filepath.Join(root, "skills")
	skillFile := filepath.Join(skills, "s1", "SKILL.md")
	for _, dir := range []string{root, filepath.Dir(skillFile)} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	if err := os.WriteFile(memory, []byte("original memory"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skillFile, []byte("original skill"), 0o644); err != nil {
		t.Fatal(err)
	}

	isolator := &Isolator{
		ReadPaths:         []string{root},
		WritePaths:        []string{root},
		BlockedWritePaths: []string{memory, skills},
		WorkDir:           root,
		BaseDir:           base,
	}
	script := fmt.Sprintf(`
echo work-ok > %[1]s/work.txt || exit 40
if echo evil > %[1]s/MEMORY.md 2>/dev/null; then exit 41; fi
rm -f %[1]s/MEMORY.md 2>/dev/null; [ -f %[1]s/MEMORY.md ] || exit 42
mv %[1]s/MEMORY.md %[1]s/MEMORY.bak 2>/dev/null; [ -f %[1]s/MEMORY.md ] || exit 43
ln -sf %[1]s/work.txt %[1]s/MEMORY.md 2>/dev/null; [ ! -L %[1]s/MEMORY.md ] || exit 44
ln -s %[1]s/MEMORY.md %[1]s/alias.md 2>/dev/null
if echo evil2 > %[1]s/alias.md 2>/dev/null; then exit 45; fi
rm -rf %[1]s/skills 2>/dev/null; [ -f %[1]s/skills/s1/SKILL.md ] || exit 46
mv %[1]s/skills %[1]s/skills_old 2>/dev/null; [ -d %[1]s/skills ] || exit 47
if echo evil3 > %[1]s/skills/s1/SKILL.md 2>/dev/null; then exit 48; fi
if mkdir %[1]s/skills/new 2>/dev/null; then exit 49; fi
mv %[1]s %[1]s_moved 2>/dev/null; [ -d %[1]s ] || exit 50
exit 0
`, root)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd, cleanup, err := isolator.ExecuteIsolated(ctx, "sh", []string{"-c", script})
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		t.Skipf("ExecuteIsolated could not select any backend in this environment (%v); run it on a host with the sandbox", err)
	}
	if output, runErr := cmd.CombinedOutput(); runErr != nil {
		t.Fatalf("a protected path was changed from inside the sandbox: %v\noutput: %s", runErr, output)
	}
	if got, _ := os.ReadFile(memory); string(got) != "original memory" {
		t.Errorf("SECURITY VIOLATION: MEMORY.md = %q", got)
	}
	if got, _ := os.ReadFile(skillFile); string(got) != "original skill" {
		t.Errorf("SECURITY VIOLATION: skill file = %q", got)
	}
	if _, statErr := os.Stat(filepath.Join(root, "work.txt")); statErr != nil {
		t.Errorf("an ordinary working file must be writable: %v", statErr)
	}
	if _, statErr := os.Stat(root + "_moved"); statErr == nil {
		t.Error("SECURITY VIOLATION: the project folder was moved away")
	}
}
