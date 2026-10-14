package server

import (
	"reflect"
	"testing"

	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
)

// A Relay run folder holds its own subfolders (.relay_dbos, .relay_ipc, .tmp). Read as groups they replaced the run in the
// Execution Logs selector ("No runs yet" for a completed run); the Relay listing must stay flat.
func TestRelayRunFolderListingIsFlat(t *testing.T) {
	run := virtualtools.WorkspaceFolderItem{
		Type: "folder", FilePath: "Workflow/relay-a/runs/iteration-22-hook",
		Children: []virtualtools.WorkspaceFolderItem{
			{Type: "folder", FilePath: "Workflow/relay-a/runs/iteration-22-hook/.relay_dbos"},
			{Type: "folder", FilePath: "Workflow/relay-a/runs/iteration-22-hook/.tmp"},
		},
	}
	children := []virtualtools.WorkspaceFolderItem{run}
	if got, want := extractIterationFoldersFromTypedChildren(children, []string{}, true), []string{"iteration-22-hook"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("relay listing = %v, want %v", got, want)
	}
	if got := extractIterationFoldersFromTypedChildren(children, []string{}); len(got) != 2 || got[0] == "iteration-22-hook" {
		t.Fatalf("a workflow with groups lists the groups instead of the parent; got %v", got)
	}
}
