package claudecode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClaudeAccountConfigAndTranscriptStayInSelectedHome(t *testing.T) {
	global, account := t.TempDir(), t.TempDir()
	t.Setenv("HOME", global)
	workdir := t.TempDir()
	prepareClaudeUserConfig(workdir, "token-B", account)
	if _, err := os.Stat(filepath.Join(global, ".claude.json")); !os.IsNotExist(err) {
		t.Fatal("selected account changed global Claude config")
	}
	if _, err := os.Stat(filepath.Join(account, ".claude", ".claude.json")); err != nil {
		t.Fatal(err)
	}
	id := "11111111-1111-4111-8111-111111111111"
	for _, home := range []string{global, account} {
		path := claudeTranscriptWorkingDirCandidates(home, workdir, id)[0]
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("{}\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	globalPath, _ := resolveClaudeTranscriptPath(id, workdir, true)
	accountPath, err := resolveClaudeTranscriptPath(id, workdir, true, account)
	if err != nil || accountPath == globalPath || !strings.HasPrefix(accountPath, account+string(os.PathSeparator)) {
		t.Fatal("selected account read the ambient transcript")
	}
}
