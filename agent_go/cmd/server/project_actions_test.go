package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// PLAT-746-1006: a project-controlled command cannot pick another project or
// replace managed metadata through a symlink. Saving never executes a command.
func TestProjectActionsConfineConfigAndWorkingFolder(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	config, revision, err := readProjectActions(root)
	if err != nil || len(config.Actions) != 0 {
		t.Fatal(err)
	}
	config.Actions = []projectAction{{ID: "dev", Name: "Dev server", Command: "printf 'not executed'", Folder: ".", Shortcut: 1, StopWhenIdle: true}}
	next, err := saveProjectActions(root, revision, config)
	if err != nil || next == revision {
		t.Fatalf("save: %v", err)
	}
	loaded, current, err := readProjectActions(root)
	if err != nil || loaded.Actions[0].Command != config.Actions[0].Command || current != next {
		t.Fatal("saved actions did not round trip")
	}
	if _, err = saveProjectActions(root, revision, config); err == nil {
		t.Fatal("stale editor replaced actions")
	}
	if err = os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if _, err = projectActionFolder(root, projectAction{Folder: "escape"}); err == nil {
		t.Fatal("escaping working folder admitted")
	}
	if _, err = projectActionFolder(root, loaded.Actions[0]); err != nil {
		t.Fatal("own folder refused", err)
	}
	linked := t.TempDir()
	if err = os.Symlink(outside, filepath.Join(linked, ".agentworks")); err != nil {
		t.Fatal(err)
	}
	if _, _, err = readProjectActions(linked); err == nil {
		t.Fatal("metadata directory symlink admitted")
	}
	if err = os.Remove(filepath.Join(root, projectActionsFile)); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(filepath.Join(outside, "other.json"), filepath.Join(root, projectActionsFile)); err != nil {
		t.Fatal(err)
	}
	if _, _, err = readProjectActions(root); err == nil {
		t.Fatal("metadata file symlink admitted")
	}
}
func TestProjectActionsRejectAmbiguousOrUnboundedDefinitions(t *testing.T) {
	for _, raw := range []string{
		`{"version":1,"actions":[{"id":"dev","name":"Dev","command":"echo ok","folder":"../other"}]}`,
		`{"version":1,"actions":[{"id":"dev","name":"Dev","command":"echo ok","folder":".agentworks"}]}`,
		`{"version":1,"actions":[{"id":"dev","name":"Dev","command":"echo ok"},{"id":"dev","name":"Other","command":"echo no"}]}`,
		`{"version":1,"actions":[],"incoming_path":"/other"}`,
		`{"version":1,"actions":[]} {"version":1,"actions":[]}`,
		strings.Repeat("x", projectActionsMaxBytes+1),
	} {
		if _, err := parseProjectActions([]byte(raw)); err == nil {
			t.Fatalf("invalid definition accepted: %.80s", raw)
		}
	}
}
