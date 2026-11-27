package clisandbox

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/internal/slotfs"
	"github.com/manishiitg/multi-llm-provider-go/pkg/codingready"
)

// TestMCPBridgeSlotLifecycleE2E exercises the cross-module contract between
// generated MCP bridge config JSON, clisandbox.JSONFilePaths, slotfs.GrantRuntime
// under simulated cross-UID slot ownership, and codingready's cold-launch marker
// lifecycle across all three session transitions:
//
//  1. Turn 1 (cold launch): stale marker cleared before launch; JSONFilePaths +
//     GrantRuntime succeed; WaitForMCPReadyFileAfter waits for mcpbridge write.
//  2. Turn 2 (warm reuse): 0600 slot-owned marker stays on disk; JSONFilePaths
//     discovers it and GrantRuntime skips setfacl without failing.
//  3. Turn 3 (cold relaunch after crash/idle eviction without CloseSession):
//     PrepareColdLaunch clears the leftover 0600 marker so WaitForMCPReadyFileAfter
//     does not return early on the dead bridge's marker.
//  4. Unremovable marker fallback: WaitForMCPReadyFileAfter requires mtime > PriorModTime.
func TestMCPBridgeSlotLifecycleE2E(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix slot ownership and setfacl semantics only")
	}

	// Install a trap setfacl binary that fails with Operation not permitted if
	// invoked on any slot-owned path, reproducing isolated-user Linux servers.
	fakeBin := t.TempDir()
	fakeSetfacl := filepath.Join(fakeBin, "setfacl")
	if err := os.WriteFile(fakeSetfacl, []byte("#!/bin/sh\necho 'setfacl: Operation not permitted' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))

	restoreUID := slotfs.SetLookupSlotUIDForTest(func(string) (string, error) {
		return strconv.Itoa(os.Getuid()), nil
	})
	defer restoreUID()

	runtimeDir := t.TempDir()
	if err := os.Chmod(runtimeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	toolOutputDir := filepath.Join(runtimeDir, "tool_output_folder")
	if err := os.MkdirAll(toolOutputDir, 0o700); err != nil {
		t.Fatal(err)
	}
	toolsFile := filepath.Join(runtimeDir, "mcp-tools.json")
	if err := os.WriteFile(toolsFile, []byte(`[{"name":"search_tools","type":"virtual"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	readyFile := filepath.Join(runtimeDir, ".mcpbridge-ready-chat1.marker")
	logDir := filepath.Join(runtimeDir, "bridge-logs")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		t.Fatal(err)
	}
	logFile := filepath.Join(logDir, "mcpbridge.log")
	if err := os.WriteFile(logFile, []byte("started\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Write realistic .cursor/mcp.json and .mcp.json matching coding_agents_bridge.go.
	mcpConfig := map[string]any{
		"mcpServers": map[string]any{
			"api-bridge": map[string]any{
				"command": "/usr/local/bin/mcpbridge",
				"args":    []string{},
				"env": map[string]string{
					"MCP_API_URL":         "http://127.0.0.1:8000",
					"MCP_API_TOKEN":       "session-token",
					"MCP_SESSION_ID":      "chat-1",
					"MCP_TOOLS_FILE":      toolsFile,
					"MCP_TOOL_OUTPUT_DIR": toolOutputDir,
					"MCP_BRIDGE_LOG":      logFile,
					"MCP_READY_FILE":      readyFile,
				},
			},
		},
	}
	rawCfg, err := json.Marshal(mcpConfig)
	if err != nil {
		t.Fatal(err)
	}
	cursorDir := filepath.Join(runtimeDir, ".cursor")
	if err := os.MkdirAll(cursorDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cursorMCP := filepath.Join(cursorDir, "mcp.json")
	claudeMCP := filepath.Join(runtimeDir, ".mcp.json")
	for _, path := range []string{cursorMCP, claudeMCP} {
		if err := os.WriteFile(path, rawCfg, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	grantAllJSONPaths := func(t *testing.T, label string) []string {
		t.Helper()
		paths := JSONFilePaths([]string{cursorMCP, claudeMCP})
		for _, p := range paths {
			if err := slotfs.GrantRuntime("slot01", p, false); err != nil {
				t.Fatalf("%s: GrantRuntime(slot01, %q) failed: %v", label, p, err)
			}
		}
		return paths
	}

	// 1. Turn 1 — cold launch: no marker on disk yet.
	marker1 := codingready.PrepareColdLaunch(readyFile)
	grantAllJSONPaths(t, "turn1_cold")
	if codingready.WaitForMCPReadyFileAfter(context.Background(), marker1, 150*time.Millisecond) {
		t.Fatal("turn1_cold: WaitForMCPReadyFileAfter returned true before mcpbridge wrote marker")
	}
	// Simulate mcpbridge running as slot01 writing 0600 marker on tools/list.
	go func() {
		time.Sleep(100 * time.Millisecond)
		_ = os.WriteFile(readyFile, []byte("ready\n"), 0o600)
	}()
	if !codingready.WaitForMCPReadyFileAfter(context.Background(), marker1, 2*time.Second) {
		t.Fatal("turn1_cold: WaitForMCPReadyFileAfter timed out waiting for mcpbridge marker")
	}

	// 2. Turn 2 — warm reuse: 0600 slot-owned marker is on disk and picked up by JSONFilePaths.
	paths2 := grantAllJSONPaths(t, "turn2_warm")
	foundMarker := false
	for _, p := range paths2 {
		if p == readyFile {
			foundMarker = true
			break
		}
	}
	if !foundMarker {
		t.Fatalf("turn2_warm: expected JSONFilePaths to include existing ready marker %q, got %v", readyFile, paths2)
	}
	if _, err := os.Stat(readyFile); err != nil {
		t.Fatalf("turn2_warm: live marker was unexpectedly removed: %v", err)
	}

	// 3. Turn 3 — cold relaunch after crash/idle eviction (no CloseSession):
	// stale 0600 marker is on disk before PrepareColdLaunch.
	marker3 := codingready.PrepareColdLaunch(readyFile)
	if marker3.HadPrior {
		t.Fatalf("turn3_cold_relaunch: expected writable dir to unlink stale marker, got %+v", marker3)
	}
	grantAllJSONPaths(t, "turn3_cold_relaunch")
	if codingready.WaitForMCPReadyFileAfter(context.Background(), marker3, 150*time.Millisecond) {
		t.Fatal("turn3_cold_relaunch: stale marker must not satisfy cold relaunch before new bridge writes")
	}
	go func() {
		time.Sleep(100 * time.Millisecond)
		_ = os.WriteFile(readyFile, []byte("ready-turn3\n"), 0o600)
	}()
	if !codingready.WaitForMCPReadyFileAfter(context.Background(), marker3, 2*time.Second) {
		t.Fatal("turn3_cold_relaunch: did not wake when new bridge wrote marker")
	}

	// 4. Fallback when a leftover 0600 marker could not be unlinked before launch:
	// mtime must advance past PriorModTime, and GrantRuntime still succeeds.
	oldTime := time.Now().Add(-5 * time.Second).Truncate(time.Second)
	if err := os.Chtimes(readyFile, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	grantAllJSONPaths(t, "turn4_unremovable_stale")
	marker4 := codingready.ColdLaunchMarker{
		Path:         readyFile,
		HadPrior:     true,
		PriorModTime: oldTime,
	}
	if codingready.WaitForMCPReadyFileAfter(context.Background(), marker4, 150*time.Millisecond) {
		t.Fatal("turn4_unremovable_stale: unchanged mtime must not satisfy WaitForMCPReadyFileAfter")
	}
	go func() {
		time.Sleep(100 * time.Millisecond)
		_ = os.WriteFile(readyFile, []byte("ready-turn4\n"), 0o600)
	}()
	if !codingready.WaitForMCPReadyFileAfter(context.Background(), marker4, 2*time.Second) {
		t.Fatal("turn4_unremovable_stale: newer mtime write should satisfy WaitForMCPReadyFileAfter")
	}

	// Negative control: if a file is NOT owned by the slot, GrantRuntime must still
	// invoke setfacl (verified via our trap binary).
	restoreOther := slotfs.SetLookupSlotUIDForTest(func(string) (string, error) {
		return "999999", nil
	})
	defer restoreOther()
	err = slotfs.GrantRuntime("slot01", readyFile, false)
	if err == nil || !strings.Contains(err.Error(), "Operation not permitted") {
		t.Fatalf("expected trap setfacl failure for non-slot-owned 0600 file, got: %v", err)
	}
}
