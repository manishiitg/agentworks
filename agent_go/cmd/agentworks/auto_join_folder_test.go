package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// #2820: a new chat joins a Team only from inside that Team's folder on this Mac, and only for the server it is
// attached to. A synced Team copy maps to its server project; a Mac-owned Crew only to this Mac's own app.
func TestAutoJoinMapsFolderToItsOwnServerTeamOnly(t *testing.T) {
	state := t.TempDir()
	t.Setenv("AGENTWORKS_STATE_ROOT", state)
	team, own := filepath.Join(t.TempDir(), "Team"), filepath.Join(t.TempDir(), "Mine")
	for _, dir := range []string{filepath.Join(team, "team", "src"), own} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	records := map[string]any{"projects": map[string]any{
		"a": crewroot.Record{Product: "work", Folder: "team-a", ID: "local-team", LocalRoot: team},
		"b": crewroot.Record{Product: "work", Folder: "mine-b", ID: "local-own", LocalRoot: own},
	}}
	if err := fsutil.WriteJSONAtomic(filepath.Join(state, "ownership", "projects.json"), records, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := crewroot.WriteSync("local-team", crewroot.SyncBinding{PairID: "p", Side: "local", Editing: "local", PeerProjectID: "server-team", PeerServer: "https://agents.example.com"}); err != nil {
		t.Fatal(err)
	}
	cases := []struct{ server, cwd, want string }{
		{"https://agents.example.com/", filepath.Join(team, "team", "src"), "server-team"},
		{"https://other.example.com", filepath.Join(team, "team"), ""},
		{"http://127.0.0.1:8000", own, "local-own"},
		{"https://agents.example.com", own, ""},
		{"https://agents.example.com", t.TempDir(), ""},
	}
	for _, c := range cases {
		if got := teamProjectForFolder(c.server, c.cwd); got != c.want {
			t.Errorf("server=%s cwd=%s: got %q, want %q", c.server, c.cwd, got, c.want)
		}
	}
}
