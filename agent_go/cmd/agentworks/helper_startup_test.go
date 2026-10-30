package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
)

func init() {
	if root := os.Getenv("AGENTWORKS_STARTUP_TEST_CHILD"); root != "" {
		err := agentworksclient.ServeLocalBridge(context.Background(), root)
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// PLAT-856-415: a real child startup failure must show fresh stderr, not an
// older attempt's error; the support section must bound and redact its tail.
func TestHelperStartupShowsFreshFailureAndRedactedDebugTail(t *testing.T) {
	root, err := os.MkdirTemp("", "aw-start-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	if err = agentworksclient.PrepareLocalBridgeRoot(root); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "sessions.json"), []byte("{invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "startup.log")
	if err = os.WriteFile(path, []byte("old attempt must not be reported\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTWORKS_STARTUP_TEST_CHILD", root)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = ensureLocalBridge(ctx, root, &options{configPath: filepath.Join(root, "no-login")})
	if err == nil || !strings.Contains(err.Error(), "invalid character") || strings.Contains(err.Error(), "old attempt") {
		t.Fatalf("current child startup cause was lost or stale: %v", err)
	}
	var raw strings.Builder
	for i := 0; i < 25; i++ {
		fmt.Fprintf(&raw, "line-%02d token=fixture-secret-value\n", i)
	}
	if err = os.WriteFile(path, []byte(raw.String()), 0600); err != nil {
		t.Fatal(err)
	}
	var report strings.Builder
	debugHelperStartup(&report, root)
	if strings.Contains(report.String(), "fixture-secret-value") || strings.Contains(report.String(), "line-04") || !strings.Contains(report.String(), "line-05") || !strings.Contains(report.String(), "line-24") {
		t.Fatalf("startup support tail leaked a value or was not bounded: %q", report.String())
	}
	// A range beginning inside a huge credential must not echo its suffix.
	if err = os.WriteFile(path, []byte("token="+strings.Repeat("z", 70<<10)), 0600); err != nil {
		t.Fatal(err)
	}
	if got := helperStartupDetail(path, 0); strings.Contains(got, "zzzz") {
		t.Fatalf("partial first line exposed a credential suffix: %q", got)
	}
}
