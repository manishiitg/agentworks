package handlers

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/manishiitg/coding-agent-loop/workspace/models"
)

// A trusted log writer must not turn a read grant or a blocked settings path
// into a write grant, and may not overwrite an existing file.
func TestShellLogRespectsWriteAndDenyGrants(t *testing.T) {
	docs := t.TempDir()
	for _, dir := range []string{"runs/current", "runs/past", "planning"} {
		if err := os.MkdirAll(filepath.Join(docs, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	guard := &models.FolderGuardConfig{Enabled: true, WritePaths: []string{"runs/current", "planning"}, ReadPaths: []string{"."}, BlockedWritePaths: []string{"planning"}, BlockedPaths: []string{"runs/current/private"}}
	for _, tc := range []struct {
		path    string
		allowed bool
	}{{"runs/current/new.log", true}, {"runs/past/other.log", false}, {"planning/plan.json", false}, {"runs/current/private/data.log", false}, {"../outside.log", false}} {
		t.Run(tc.path, func(t *testing.T) {
			log, reason := prepareShellLog(models.ExecuteShellRequest{LogFile: tc.path, FolderGuard: guard}, docs, filepath.Join(docs, "runs/current"))
			if (log != nil) != tc.allowed {
				t.Fatalf("allowed=%v reason=%s", log != nil, reason)
			}
			if log != nil {
				log.Close()
				if _, reason := prepareShellLog(models.ExecuteShellRequest{LogFile: tc.path, FolderGuard: guard}, docs, docs); reason == "" {
					t.Fatal("existing file was overwritten")
				}
			}
		})
	}
}
