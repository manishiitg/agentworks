package claudecode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Claude rejects a settings file whose additionalDirectories is not an array
// ("Expected array, but received undefined") and then stops on a Settings
// Error screen, so a chat with no extra grants must still write [].
func TestClaudeMirrorLandlockReadsWritesArrays(t *testing.T) {
	home := t.TempDir()
	work := t.TempDir()
	policy := &llmtypes.CLISecurityPolicy{PrivateHome: home, WorkspaceWritePaths: []string{work}}
	// A home saved before 2026-10-20 still carries the old read block; it must go, or Bash in dontAsk mode refuses
	// commands Claude cannot prove stay inside the folders (Landlock confines them anyway).
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte(`{"permissions":{"blockReadsOutsideWorkingDirectories":true}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := claudeMirrorLandlockReads(policy, work); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var settings struct {
		Permissions struct {
			Block bool            `json:"blockReadsOutsideWorkingDirectories"`
			Dirs  json.RawMessage `json:"additionalDirectories"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatal(err)
	}
	if settings.Permissions.Block || string(settings.Permissions.Dirs) != "[]" {
		t.Fatalf("settings = %s", data)
	}
}

// A managed Claude offers only the product's skills, not its own built-ins
// (which compete with AgentWorks's schedules and dashboards).
func TestClaudeSkillScopeEnvHidesBuiltInSkills(t *testing.T) {
	env := claudeSkillScopeEnv()
	want := map[string]bool{"CLAUDE_CODE_DISABLE_BUNDLED_SKILLS=1": false, "CLAUDE_CODE_DISABLE_POLICY_SKILLS=1": false}
	for _, entry := range env {
		if _, ok := want[entry]; ok {
			want[entry] = true
		}
	}
	for entry, seen := range want {
		if !seen {
			t.Fatalf("missing %s in %v", entry, env)
		}
	}
}
