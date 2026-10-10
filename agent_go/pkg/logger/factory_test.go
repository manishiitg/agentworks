package logger

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
)

func TestCreateLoggerAlwaysAddsDiagnosticIdentity(t *testing.T) {
	path := t.TempDir() + "/structured.log"
	base, err := CreateLogger(path, "info", "text", false)
	if err != nil {
		t.Fatalf("CreateLogger: %v", err)
	}
	base.Info("startup")

	ctx := context.WithValue(context.Background(), common.UsernameKey, "customer")
	ctx = context.WithValue(ctx, common.WorkflowNameKey, "testing")
	child := WithContext(base, ctx)
	if err := child.Close(); err != nil {
		t.Fatalf("child Close: %v", err)
	}
	// A contextual child must not close the root logger's owned file.
	child.Info("request")
	if err := base.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 2 {
		t.Fatalf("log lines = %d, want 2: %q", len(lines), raw)
	}
	for _, want := range []string{`username=-`, `workflow=-`, `msg=startup`} {
		if !strings.Contains(lines[0], want) {
			t.Fatalf("startup log = %q, missing %q", lines[0], want)
		}
	}
	for _, want := range []string{`username=customer`, `workflow=testing`, `msg=request`} {
		if !strings.Contains(lines[1], want) {
			t.Fatalf("request log = %q, missing %q", lines[1], want)
		}
	}
}
