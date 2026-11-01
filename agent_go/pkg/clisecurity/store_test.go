package clisecurity

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestDefaultRootHonorsOverrideEnv(t *testing.T) {
	t.Setenv("AGENTWORKS_CLI_SECURITY_DIR", "")
	unset, err := DefaultRoot()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(unset, filepath.Join("AgentWorks", "cli-security")) {
		t.Fatalf("unset override must fall back to the shared AgentWorks root, got %q", unset)
	}

	override := filepath.Join(t.TempDir(), "example-app-dev-instance", "cli-security")
	t.Setenv("AGENTWORKS_CLI_SECURITY_DIR", override)
	got, err := DefaultRoot()
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Clean(override) {
		t.Fatalf("DefaultRoot() = %q, want the override %q", got, override)
	}
}

func TestStoreDefaultsToCompatibilityAndPersistsOutsideWorkspace(t *testing.T) {
	root := filepath.Join(t.TempDir(), "operator-state")
	store, err := NewStore(root)
	if err != nil {
		t.Fatal(err)
	}

	config, err := store.Read("user/with/path")
	if err != nil {
		t.Fatal(err)
	}
	if config.Mode != llmtypes.CLISecurityModeCompatibility {
		t.Fatalf("mode = %q, want compatibility", config.Mode)
	}

	config.Mode = llmtypes.CLISecurityModeVerified
	config.ApprovedProfiles["codex-cli"] = ProfileApproval{
		ProfileVersion: "1",
		Capabilities:   []string{"provider_identity"},
	}
	if _, err := store.Write("user/with/path", config); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() == "path" {
		t.Fatalf("unexpected user-derived path entries: %#v", entries)
	}
	info, err := os.Stat(filepath.Join(root, entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 600", info.Mode().Perm())
	}
}

func TestResolveCompatibilityIsBackwardCompatible(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	policy, err := store.Resolve("user", "codex-cli", []string{"/workspace"}, []string{"/workspace/out"})
	if err != nil {
		t.Fatal(err)
	}
	if policy.Mode != llmtypes.CLISecurityModeCompatibility || policy.Provider != "codex-cli" {
		t.Fatalf("unexpected policy: %#v", policy)
	}
}

func TestResolveStrictModeFailsClosedUntilProfileCertified(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	config := DefaultConfig()
	config.Mode = llmtypes.CLISecurityModeVerified
	if _, err := store.Write("user", config); err != nil {
		t.Fatal(err)
	}
	_, err = store.Resolve("user", "codex-cli", nil, nil)
	if !errors.Is(err, ErrModeNotEnforceable) {
		t.Fatalf("error = %v, want ErrModeNotEnforceable", err)
	}
}

func TestStoreRejectsUnknownModeInsteadOfFallingBackToCompatibility(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	config := DefaultConfig()
	config.Mode = "future-unknown-mode"
	if _, err := store.Write("user", config); err == nil {
		t.Fatal("expected unknown mode to be rejected")
	}
}

func TestResolveCertifiedCodexVerifiedProfile(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	config := DefaultConfig()
	config.Mode = llmtypes.CLISecurityModeVerified
	config.ApprovedProfiles["codex-cli"] = ProfileApproval{
		ProfileVersion: "1",
		Capabilities:   []string{"provider_identity"},
	}
	if _, err := store.Write("user", config); err != nil {
		t.Fatal(err)
	}
	policy, err := store.Resolve("user", "codex-cli", []string{"/workspace"}, []string{"/workspace"})
	if err != nil {
		t.Fatal(err)
	}
	if policy.ProfileVersion != "1" || len(policy.HostReadPaths) != 1 || filepath.Base(policy.HostReadPaths[0]) != ".codex" {
		t.Fatalf("unexpected verified policy: %#v", policy)
	}
}

func TestResolveCodexIsolatedProfileUsesManagedPrivateHome(t *testing.T) {
	root := t.TempDir()
	store, err := NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	config := DefaultConfig()
	config.Mode = llmtypes.CLISecurityModeIsolated
	if _, err := store.Write("user", config); err != nil {
		t.Fatal(err)
	}
	policy, err := store.Resolve("user", "codex-cli", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if policy.PrivateHome == "" || !pathWithinRoot(policy.PrivateHome, root) {
		t.Fatalf("private home escaped store root: %q", policy.PrivateHome)
	}
	if len(policy.HostReadPaths) != 0 || len(policy.HostWritePaths) != 0 {
		t.Fatalf("isolated policy exposed host paths: %#v", policy)
	}
}

func pathWithinRoot(path, root string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
