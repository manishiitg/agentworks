//go:build linux

package handlers

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/manishiitg/coding-agent-loop/workspace/models"
)

func TestInitializeDBKeepsProjectDirectoryWritable(t *testing.T) {
	rel, abs, router := setupWorkflowDBTest(t)
	project := filepath.Dir(filepath.Dir(abs))
	if err := os.Chmod(project, os.ModeSetgid|0o770); err != nil {
		t.Fatal(err)
	}
	// Reproduce the service-created, traversable but non-writable db/.
	if err := os.Chmod(filepath.Dir(abs), os.ModeSetgid|0o750); err != nil {
		t.Fatal(err)
	}
	recorder := postWorkflowDBTest(t, router, "/api/db/initialize", models.InitializeDatabaseRequest{DBPath: rel})
	if recorder.Code != http.StatusOK {
		t.Fatalf("initialize status=%d %s", recorder.Code, recorder.Body)
	}
	info, err := os.Stat(filepath.Dir(abs))
	if err != nil || info.Mode().Perm() != 0o770 || info.Mode()&os.ModeSetgid == 0 {
		t.Fatalf("db directory = %v, %v", info, err)
	}
	if err := os.Mkdir(filepath.Join(project, "db", "migrations"), 0o770); err != nil {
		t.Fatal(err)
	}
}
