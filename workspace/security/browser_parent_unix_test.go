//go:build unix

package security

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/browserconfig"
)

func TestExecuteIsolatedRefusesUnsafeManagedBrowserParent(t *testing.T) {
	t.Setenv("AGENTWORKS_BROWSER_STAGING_NAMESPACE", fmt.Sprintf("parent-refusal-%d", time.Now().UnixNano()))
	root := browserconfig.ManagedSocketRoot()
	outside := t.TempDir()
	if err := os.Symlink(outside, root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(root) })
	session := "workflow-0123456789abcdef--browser"
	iso := &Isolator{BaseDir: t.TempDir(), BrowserSession: session}
	cmd, cleanup, err := iso.ExecuteIsolated(context.Background(), "sh", []string{"-c", "exit 0"})
	if cleanup != nil {
		cleanup()
	}
	if err == nil || cmd != nil || !strings.Contains(err.Error(), "prepare managed browser scope") {
		t.Fatalf("unsafe parent must refuse command construction: cmd=%v err=%v", cmd, err)
	}
	artifact := filepath.Join(browserconfig.ArtifactDirForSession(session), "test.png")
	if err := PrepareBrowserArtifactStaging(artifact, session); err == nil {
		t.Fatal("unsafe parent must also refuse artifact preparation")
	}
	if entries, err := os.ReadDir(outside); err != nil || len(entries) != 0 {
		t.Fatalf("the symlink target was modified: %v %v", entries, err)
	}
}
