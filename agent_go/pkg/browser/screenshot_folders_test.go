package browser

import (
	"context"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestScreenshotFoldersDropStaleRuntimeAndKeepOnlyThisTurnsScratch(t *testing.T) {
	session := "screenshot-folders-current"
	defer common.ClearSessionShellConfig(session)
	root := t.TempDir()
	runtime := filepath.Join(root, "cli-runtimes", "v1", "old-session")
	if err := os.MkdirAll(runtime, 0700); err != nil {
		t.Fatal(err)
	}
	project := t.TempDir()
	scratch := t.TempDir()
	common.SetSessionFolderGuard(session, []string{project, runtime}, []string{project, runtime, filepath.Join(root, "gone")})
	common.SetSessionBrowserScratch(session, scratch)
	opts, _, err := browserExecuteOptions(context.Background(), session, "", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(opts.FolderGuard.WritePaths) != 2 || opts.FolderGuard.WritePaths[0] != project || opts.FolderGuard.WritePaths[1] != scratch {
		t.Fatalf("wrong screenshot roots: %v", opts.FolderGuard.WritePaths)
	}
	next := t.TempDir()
	common.SetSessionBrowserScratch(session, next)
	opts, _, err = browserExecuteOptions(context.Background(), session, "", time.Second)
	if err != nil || len(opts.FolderGuard.WritePaths) != 2 || opts.FolderGuard.WritePaths[1] != next {
		t.Fatalf("old-turn scratch retained: %+v %v", opts, err)
	}
}
