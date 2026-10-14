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
	// A normal workflow run with hidden working folders next to its group folders: only the groups are listed.
	grouped := virtualtools.WorkspaceFolderItem{
		Type: "folder", FilePath: "Workflow/wf-a/runs/iteration-3",
		Children: []virtualtools.WorkspaceFolderItem{
			{Type: "folder", FilePath: "Workflow/wf-a/runs/iteration-3/group-1"},
			{Type: "folder", FilePath: "Workflow/wf-a/runs/iteration-3/.tmp"},
		},
	}
	if got, want := extractIterationFoldersFromTypedChildren([]virtualtools.WorkspaceFolderItem{grouped}, []string{}), []string{"iteration-3/group-1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("hidden folders counted as groups: %v, want %v", got, want)
	}
	// Only hidden subfolders: no groups, so the run itself is listed.
	if got, want := extractIterationFoldersFromTypedChildren(children, []string{}), []string{"iteration-22-hook"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("a run with only hidden subfolders = %v, want %v", got, want)
	}
}
