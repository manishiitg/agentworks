package codingsdks

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// This runs only on the build host, using the same embedded installer as the
// packaged backend. It installs real SDKs but never calls a model or agent.
func TestRealCodingSDKProvisioning(t *testing.T) {
	if os.Getenv("AGENTWORKS_TEST_REAL_CODING_SDKS") != "1" {
		t.Skip("real SDK install is opt-in on the build host")
	}
	t.Setenv("CLAUDE_AGENT_SDK_PATH", "")
	t.Setenv("CURSOR_SDK_PATH", "")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	m, err := New(ctx, filepath.Join(t.TempDir(), "sdk components"), true, t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	m.Start()
	for provider, variable := range map[string]string{"claude-code": "CLAUDE_AGENT_SDK_PATH", "cursor-cli": "CURSOR_SDK_PATH"} {
		if err := m.Ensure(ctx, provider); err != nil {
			t.Fatal(err)
		}
		entry := os.Getenv(variable)
		info, err := os.Stat(entry)
		if err != nil || !filepath.IsAbs(entry) {
			t.Fatalf("%s not ready: %v", provider, err)
		}
		if err := m.Ensure(ctx, provider); err != nil {
			t.Fatal(err)
		}
		after, err := os.Stat(entry)
		if err != nil || !info.ModTime().Equal(after.ModTime()) {
			t.Fatalf("healthy %s was reinstalled", provider)
		}
		t.Logf("%s entry=%s; verified/imported without inference", provider, entry)
	}
}

func TestFailedSDKSetupRetriesWithoutLeakingSecrets(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node required for embedded provisioner")
	}
	entry := filepath.Join(t.TempDir(), "configured sdk.mjs")
	t.Setenv("CLAUDE_AGENT_SDK_PATH", entry)
	t.Setenv("SECRET_SDK_SETUP_TEST", "must-not-reach-installer")
	m, err := New(context.Background(), t.TempDir(), false, t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Ensure(context.Background(), "claude-code"); err == nil || !strings.Contains(err.Error(), "configured component") {
		t.Fatalf("missing explicit SDK: %v", err)
	}
	if err := os.WriteFile(entry, []byte("if (process.env.SECRET_SDK_SETUP_TEST) throw Error('unexpected secret'); export function query() {}"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.Ensure(context.Background(), "claude-code"); err != nil {
		t.Fatalf("retry after repair: %v", err)
	}
}
