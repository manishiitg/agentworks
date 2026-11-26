package picli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPiLandlockWritesTheLaunchFolder(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "launch-pi.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	got := piLandlockWrites([]string{"env", "--config", script})
	if len(got) != 1 || got[0] != dir {
		t.Fatalf("writes = %v, want [%s]", got, dir)
	}
	if got := piLandlockWrites([]string{"pi", "--no-approve"}); len(got) != 0 {
		t.Fatalf("a launch without a script granted %v", got)
	}
}

// Pi's bridge program is named in its private mcp.json and is granted from there.
func TestPiLandlockReadsGrantsTheBridgeNamedInItsMCPConfig(t *testing.T) {
	root := t.TempDir()
	bridge := filepath.Join(root, "bin", "mcpbridge")
	if err := os.MkdirAll(filepath.Dir(bridge), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bridge, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	agentDir := filepath.Join(root, "agent")
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	config := `{"mcpServers":{"api-bridge":{"command":"` + bridge + `","args":[]}}}`
	if err := os.WriteFile(filepath.Join(agentDir, "mcp.json"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	got := piLandlockReads([]string{"/x/launch-pi.sh"}, "OTHER=1", "PI_CODING_AGENT_DIR="+agentDir)
	found := false
	for _, path := range got {
		if path == bridge {
			found = true
		}
	}
	if !found {
		t.Fatalf("reads %v do not include the bridge %s", got, bridge)
	}
}
