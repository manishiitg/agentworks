package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/crewsync"
)

// PLAT-746-809: protected old clash/current bases must not make a pruned gap
// look like a complete readable history; time and event-count retention combine.
func TestTeamRetentionKeepsClashBasesWithoutHidingHistoryGaps(t *testing.T) {
	now := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	for _, recent := range []bool{false, true} {
		t.Run(fmt.Sprint(recent), func(t *testing.T) {
			state := crewTeamFileState{Revision: 600, Versions: map[string]crewTeamVersion{}, Clashes: []crewTeamClash{{Path: "clash.txt", WinningRevision: 3, LosingRevision: 2}}}
			for i := 1; i <= 600; i++ {
				path := "current.txt"
				if i == 1 {
					path = "anchor.txt"
				}
				if i == 2 || i == 3 {
					path = "clash.txt"
				}
				at := now.Add(-40 * 24 * time.Hour)
				if recent && i == 50 {
					at = now.Add(-29 * 24 * time.Hour)
				}
				file := &crewsync.File{Hash: crewTransferDigest(fmt.Sprint(i)), Size: 1}
				state.Events = append(state.Events, crewTeamEvent{Path: path, Revision: uint64(i), At: at.Format(time.RFC3339Nano), File: file})
				if i == 1 || i == 600 {
					state.Versions[path] = crewTeamVersion{File: file, Revision: uint64(i)}
				}
			}
			removed, _ := state.retainHistory(now)
			expected := uint64(100)
			if recent {
				expected = 49
			}
			if removed == 0 || state.HistoryFloor != expected {
				t.Fatalf("wrong history boundary %d (want %d), removed %d", state.HistoryFloor, expected, removed)
			}
			if state.versionAt("anchor.txt", 1) == nil || state.versionAt("clash.txt", 2) == nil || state.versionAt("clash.txt", 3) == nil {
				t.Fatal("protected current or clash side was lost")
			}
			for _, e := range state.Events {
				if e.Revision == 20 {
					t.Fatal("unprotected old gap was retained")
				}
			}
		})
	}
}

// Real Mac protocol path over the actual Team HTTP handlers, local files and
// chunk store. Catch-up preserves unsent local work and requires a normal delta
// before declaring its base current; an old upload must not reach the file store.
func TestTeamCatchUpPreservesLocalEditsBeforeTheNextUpload(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	t.Setenv("MULTI_USER_MODE", "true")
	crewTeamTestDocs(t, "catch-up", "catch-up")
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","can_create":true,"products":["work"]}]}`)
	if err := defaultProjectOwners().Register(projectOwnerRecord{Product: "work", Folder: "catch-up", OwnerID: "owner", ProjectID: "catch-up"}); err != nil {
		t.Fatal(err)
	}
	if err := writeCrewTeam(crewTeam{ProjectID: "catch-up", OwnerID: "owner", On: true, Folder: crewTeamFolder{ID: "f1", Folders: []string{"team"}}}); err != nil {
		t.Fatal(err)
	}
	store, err := crewTeamChunkStore("catch-up")
	if err != nil {
		t.Fatal(err)
	}
	version, err := chunkReader(strings.NewReader("server copy"), 11, false, func(id string, chunk []byte) error { return putChunk(store, id, chunk) })
	if err != nil {
		t.Fatal(err)
	}
	if _, err = commitCrewTeamChanges(t.Context(), "catch-up", "owner", "initial", crewTransferDigest("initial"), []crewTeamChange{{Path: "team/note.txt", After: &version}}); err != nil {
		t.Fatal(err)
	}
	state, err := readCrewTeamFileState("catch-up")
	if err != nil {
		t.Fatal(err)
	}
	state.Revision = 600
	state.Events = nil
	v := state.Versions["team/note.txt"]
	v.Revision = 600
	state.Versions["team/note.txt"] = v
	state.Versions["team/gone.txt"] = crewTeamVersion{Revision: 500}
	state.Versions["not-shared.txt"] = v
	old := time.Now().Add(-40 * 24 * time.Hour).UTC().Format(time.RFC3339Nano)
	for i := 1; i <= 600; i++ {
		state.Events = append(state.Events, crewTeamEvent{Path: "team/note.txt", Revision: uint64(i), Actor: "owner", At: old, File: syncFile(version), Chunks: version.Chunks})
	}
	if err = saveCrewTeamFileState("catch-up", state); err != nil {
		t.Fatal(err)
	}
	root, dir := t.TempDir(), t.TempDir()
	if err = os.MkdirAll(filepath.Join(root, "team"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "team", "note.txt"), []byte("my unsent edit"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "team", "gone.txt"), []byte("old gone"), 0600); err != nil {
		t.Fatal(err)
	}
	b := &crewTeamLocal{Root: root, Project: "catch-up", LocalProject: "catch-up", FolderID: "f1", Client: "copy", RemoteUser: "owner", Revision: 1, Downloaded: true, Files: map[string]crewTeamLocalFile{
		"team/note.txt": {Version: chunkedFile{Hash: crewTransferDigest("old baseline"), Size: 12}},
		"team/gone.txt": {Version: chunkedFile{Hash: crewTransferDigest("old gone"), Size: 8}},
	}}
	api := &StreamingAPI{}
	handler := api.crewTeamServerHandler()
	request := httptest.NewRequest(http.MethodGet, "/api/crew-teams/catch-up/history?folder_id=f1&path=team%2Fnote.txt&revision=1", nil)
	request = request.WithContext(context.WithValue(request.Context(), UserContextKey, &UserClaims{UserID: "owner", Username: "owner"}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "team_history_catch_up") || !strings.Contains(response.Body.String(), "team/gone.txt") || strings.Contains(response.Body.String(), "not-shared.txt") {
		t.Fatalf("old history was misreported or leaked folders: %d %s", response.Code, response.Body.String())
	}

	deltas := 0
	observed := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/sync/changes") {
			deltas++
			if deltas == 2 && b.Downloaded {
				t.Error("catch-up was acknowledged before a normal delta")
			}
		}
		handler.ServeHTTP(w, r)
	})
	ctx := context.WithValue(t.Context(), crewTeamTransportKey{}, http.RoundTripper(crewTeamInProcess{handler: observed, claims: &UserClaims{UserID: "owner", Username: "owner"}}))
	c := desktopConnection{ID: "server", Server: "http://team.example.com", User: UserInfo{ID: "owner"}}
	if err = syncCrewTeamRound(ctx, "owner", c, dir, b); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(root, "team", "note.txt")); string(data) != "my unsent edit" {
		t.Fatal("catch-up overwrote local edits")
	}
	if _, err = os.Stat(filepath.Join(root, "team", "gone.txt")); !os.IsNotExist(err) {
		t.Fatal("catch-up lost a tombstone")
	}
	if deltas < 2 || !b.Downloaded || b.Revision != 600 || b.Status != "pending" || b.Files["team/note.txt"].Version.Hash != version.Hash {
		t.Fatalf("catch-up did not reconcile pending edits: %+v deltas=%d", b, deltas)
	}
	if _, err = os.Stat(filepath.Join(root, "not-shared.txt")); !os.IsNotExist(err) {
		t.Fatal("catch-up disclosed an unshared file")
	}
	state, err = readCrewTeamFileState("catch-up")
	if err != nil || state.Versions["team/note.txt"].File.Hash != version.Hash {
		t.Fatal("stale upload changed canonical content")
	}
	if err = syncCrewTeamRound(ctx, "owner", c, dir, b); err != nil {
		t.Fatal(err)
	}
	state, err = readCrewTeamFileState("catch-up")
	if err != nil || state.Versions["team/note.txt"].File.Hash != crewTransferDigest("my unsent edit") || state.versionAt("team/note.txt", 600) == nil {
		t.Fatal("reconciled upload lost local work or the previous server version")
	}
}
