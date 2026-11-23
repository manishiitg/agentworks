package claudecode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWriteClaudeCodeProjectMCPFileLifecycleNoPriorContent covers the
// fresh-workspace case: no .mcp.json exists, writer creates one with
// the operator-supplied JSON, removeFiles cleans it up.
func TestWriteClaudeCodeProjectMCPFileLifecycleNoPriorContent(t *testing.T) {
	tmp := t.TempDir()
	mcpJSON := `{"mcpServers":{"api-bridge":{"command":"/usr/local/bin/mcpbridge","env":{"MCP_API_URL":"http://localhost:9000"}}}}`

	path, err := writeClaudeCodeProjectMCPFile(tmp, mcpJSON)
	if err != nil {
		t.Fatalf("writeClaudeCodeProjectMCPFile: %v", err)
	}
	if path == "" {
		t.Fatal("expected non-empty path with non-empty workingDir")
	}
	expected := filepath.Join(tmp, ".mcp.json")
	if path != expected {
		t.Errorf("path %q must equal expected %q", path, expected)
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read .mcp.json: %v", err)
	}
	if string(body) != mcpJSON {
		t.Errorf(".mcp.json must contain the operator-supplied JSON verbatim\n  want: %s\n  got:  %s", mcpJSON, body)
	}

	removeFiles([]string{path})
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("removeFiles must delete the .mcp.json we created from nothing; stat err=%v", err)
	}
}

// TestExtractClaudeMCPServerNamesParsesMCPServersKey locks in the
// shape we depend on for pre-approval: top-level "mcpServers" object
// → returned slice of its keys. Malformed JSON or missing key returns
// nil so the caller can safely no-op.
func TestExtractClaudeMCPServerNamesParsesMCPServersKey(t *testing.T) {
	cases := []struct {
		name string
		json string
		want []string
	}{
		{
			"single server",
			`{"mcpServers":{"api-bridge":{"command":"x"}}}`,
			[]string{"api-bridge"},
		},
		{
			"two servers",
			`{"mcpServers":{"alpha":{"command":"x"},"beta":{"command":"y"}}}`,
			[]string{"alpha", "beta"},
		},
		{
			"no mcpServers key",
			`{"otherKey":42}`,
			nil,
		},
		{
			"malformed JSON",
			`{not json`,
			nil,
		},
		{
			"empty mcpServers",
			`{"mcpServers":{}}`,
			[]string{},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := extractClaudeMCPServerNames(c.json)
			// Ordering not guaranteed; compare as sets.
			if len(got) != len(c.want) {
				t.Fatalf("extractClaudeMCPServerNames(%q) = %v, want %v (length mismatch)", c.json, got, c.want)
			}
			gotSet := map[string]bool{}
			for _, n := range got {
				gotSet[n] = true
			}
			for _, n := range c.want {
				if !gotSet[n] {
					t.Errorf("extractClaudeMCPServerNames(%q) missing %q; got %v", c.json, n, got)
				}
			}
		})
	}
}

// TestPreApproveClaudeMCPServersForWorkingDirIdempotent ensures
// repeated pre-approvals of the same server name don't duplicate the
// entry in enabledMcpjsonServers, and that we MERGE with any prior
// entries the operator had set manually (we never overwrite).
func TestPreApproveClaudeMCPServersForWorkingDirIdempotent(t *testing.T) {
	// Redirect ~/.claude.json to a tempdir so we don't pollute the
	// user's real config.
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	workingDir := t.TempDir()

	// Seed an operator entry with their own pre-approved server.
	configPath := filepath.Join(tmpHome, ".claude.json")
	seedConfig := `{"projects":{"` + workingDir + `":{"enabledMcpjsonServers":["operator-server"]}}}`
	if err := os.WriteFile(configPath, []byte(seedConfig), 0o600); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	// First call: add api-bridge alongside operator-server.
	preApproveClaudeMCPServersForWorkingDir(workingDir, `{"mcpServers":{"api-bridge":{"command":"x"}}}`)
	// Second call (idempotent): re-adding api-bridge must NOT duplicate it.
	preApproveClaudeMCPServersForWorkingDir(workingDir, `{"mcpServers":{"api-bridge":{"command":"x"}}}`)

	body, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read updated config: %v", err)
	}
	if !strings.Contains(string(body), `"operator-server"`) {
		t.Errorf("operator's pre-existing enabledMcpjsonServers entry must be preserved; got:\n%s", body)
	}
	if !strings.Contains(string(body), `"api-bridge"`) {
		t.Errorf("our orchestrator server name must be added to enabledMcpjsonServers; got:\n%s", body)
	}
	// Idempotency check: parse the config and assert each individual
	// enabledMcpjsonServers array contains api-bridge AT MOST ONCE.
	// On macOS the function records under BOTH the raw and symlink-
	// resolved paths (/var → /private/var), so the same name shows up
	// under both project entries — that's not a duplicate, that's
	// path aliasing.
	var doc struct {
		Projects map[string]struct {
			Enabled []string `json:"enabledMcpjsonServers"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("re-parse updated config: %v", err)
	}
	for path, p := range doc.Projects {
		seen := map[string]int{}
		for _, name := range p.Enabled {
			seen[name]++
		}
		for name, count := range seen {
			if count > 1 {
				t.Errorf("projects[%s].enabledMcpjsonServers must not contain duplicates; %q appears %d times", path, name, count)
			}
		}
	}
}

// TestWriteClaudeCodeProjectMCPFileEmptyInputsNoOp guards against the
// adapter accidentally writing .mcp.json into the orchestrator's own
// cwd (or writing an empty JSON document) when the caller forgot to
// set MetadataKeyWorkingDir or MetadataKeyMCPConfig.
func TestWriteClaudeCodeProjectMCPFileEmptyInputsNoOp(t *testing.T) {
	path, err := writeClaudeCodeProjectMCPFile("", "anything")
	if err != nil {
		t.Errorf("empty workingDir should return nil error; got %v", err)
	}
	if path != "" {
		t.Errorf("empty workingDir should return empty path; got %q", path)
	}
	if _, err := os.Stat(".mcp.json"); err == nil {
		t.Errorf(".mcp.json must NOT be created in process cwd when workingDir is empty")
		_ = os.Remove(".mcp.json")
	}

	tmp := t.TempDir()
	path, err = writeClaudeCodeProjectMCPFile(tmp, "   ")
	if err != nil {
		t.Errorf("blank mcpJSON should return nil error; got %v", err)
	}
	if path != "" {
		t.Errorf("blank mcpJSON should return empty path; got %q", path)
	}
	if _, err := os.Stat(filepath.Join(tmp, ".mcp.json")); err == nil {
		t.Errorf(".mcp.json must NOT be created when mcpJSON is blank")
	}
}
