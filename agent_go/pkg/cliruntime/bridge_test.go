package cliruntime

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBundledMCPBridgeResolvesReleaseAndCheckoutWithoutPATH(t *testing.T) {
	for _, location := range []string{"mcpbridge", ".bin/mcpbridge"} {
		t.Run(location, func(t *testing.T) {
			root := t.TempDir()
			bridge := filepath.Join(root, location)
			if err := os.MkdirAll(filepath.Dir(bridge), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(bridge, []byte("#!/bin/sh\n"), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", t.TempDir())
			if got := bundledMCPBridge(filepath.Join(root, "agent")); got != bridge {
				t.Fatalf("bridge %q, want %q", got, bridge)
			}
			if err := os.Chmod(bridge, 0600); err != nil {
				t.Fatal(err)
			}
			if got := bundledMCPBridge(filepath.Join(root, "agent")); got != "" {
				t.Fatalf("non-executable bridge accepted: %s", got)
			}
		})
	}
}
func TestBundledMCPBridgeFollowsServerReleaseSymlink(t *testing.T) {
	root := t.TempDir()
	release := filepath.Join(root, "releases", "release-1", "bin")
	if err := os.MkdirAll(release, 0700); err != nil {
		t.Fatal(err)
	}
	for _, binary := range []string{"agent", "mcpbridge"} {
		if err := os.WriteFile(filepath.Join(release, binary), []byte("#!/bin/sh\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(root, "agent")
	if err := os.Symlink(filepath.Join(release, "agent"), link); err != nil {
		t.Fatal(err)
	}
	expected, err := filepath.EvalSymlinks(filepath.Join(release, "mcpbridge"))
	if err != nil {
		t.Fatal(err)
	}
	if got := bundledMCPBridge(link); got != expected {
		t.Fatalf("wrong release bridge: %s", got)
	}
}
func TestMCPBridgeExplicitOverrideIsPreserved(t *testing.T) {
	t.Setenv("MCP_BRIDGE_BINARY", "/operator/custom/mcpbridge")
	if got := MCPBridgeBinary(); got != "/operator/custom/mcpbridge" {
		t.Fatalf("override lost: %s", got)
	}
}
