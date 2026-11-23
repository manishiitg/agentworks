package claudecode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/pkg/pathidentity"
)

func TestClaudePhysicalWorkspaceTrustAndMCPKeys(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(t.TempDir(), "agentworks")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(filepath.Dir(dir), "AgentWorks")
	if _, err := os.Stat(alias); err != nil {
		if err := os.Symlink(dir, alias); err != nil {
			t.Fatal(err)
		}
	}
	prepareClaudeUserConfig(alias, "")
	preApproveClaudeMCPServersForWorkingDir(alias, `{"mcpServers":{"fixture":{"command":"fixture-only"}}}`)
	raw, err := os.ReadFile(filepath.Join(home, ".claude.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Projects map[string]struct {
			Trusted bool     `json:"hasTrustDialogAccepted"`
			Servers []string `json:"enabledMcpjsonServers"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	physical, err := pathidentity.Resolve(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{alias, physical} {
		project := doc.Projects[path]
		if !project.Trusted || len(project.Servers) != 1 || project.Servers[0] != "fixture" {
			t.Fatalf("missing trust/MCP approval for %s", path)
		}
	}
	const id = "00000000-0000-4000-8000-000000000001"
	candidates := claudeTranscriptWorkingDirCandidates(home, physical, id)
	if len(candidates) == 0 {
		t.Fatal("no transcript candidate")
	}
	if err := os.MkdirAll(filepath.Dir(candidates[0]), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(candidates[0], []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	found, err := resolveClaudeTranscriptPath(id, alias, false)
	if err != nil || found == "" {
		t.Fatalf("physical transcript not found without global fallback: %v", err)
	}
}
