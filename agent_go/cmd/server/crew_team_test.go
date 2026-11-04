package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/workspace/crewsync"
)

// PLAT-856-509: actual HTTP/file paths, with an allowed edit before each refusal.
// Folder membership never grants the source owner's private Crew authority.
func TestCrewTeamFolderHTTPConflictRetryAndRevocation(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	t.Setenv("MULTI_USER_MODE", "true")
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","can_create":true,"products":["work"]},{"id":"member","username":"member","can_create":true,"products":["work"]},{"id":"teammate","username":"teammate","can_create":true,"products":["work"]},{"id":"outsider","username":"outsider","can_create":true,"products":["work"]}]}`)
	if err := defaultProjectOwners().Register(projectOwnerRecord{Product: "work", Folder: "team-test", OwnerID: "owner", ProjectID: "team-test"}); err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{}
	router := mux.NewRouter()
	api.registerCrewTeamRoutes(router.PathPrefix("/api").Subrouter())
	call := func(user, method, route, body, transfer string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, route, strings.NewReader(body))
		if !r.URL.Query().Has("folder_id") {
			if team, err := readCrewTeam("team-test"); err == nil {
				q := r.URL.Query()
				q.Set("folder_id", team.Folder.ID)
				r.URL.RawQuery = q.Encode()
			}
		}
		r = r.WithContext(context.WithValue(r.Context(), UserContextKey, &UserClaims{UserID: user, Username: user}))
		r.Header.Set("X-Team-Transfer", transfer)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	base := "/api/crew-teams/team-test"
	// Team off (the Team tab's switch): the saved roster grants nothing, folder or agents.
	w := call("owner", "PUT", base, `{"name":"Example","people":[{"user_id":"member"}],"folder":{"path":"docs"}}`, "")
	if w.Code != 200 || crewTeamMember("team-test", "member") || call("member", "GET", base+"/files", "", "").Code != 403 {
		t.Fatalf("a roster saved with Team off must not let its people in: %d %s", w.Code, w.Body.String())
	}
	config := `{"name":"Example","on":true,"people":[{"user_id":"member"},{"user_id":"teammate"}],"folder":{"path":"docs"}}`
	w = call("owner", "PUT", base, config, "")
	if w.Code != 200 {
		t.Fatalf("owner config: %d %s", w.Code, w.Body.String())
	}
	// Added people are invited, not members, until they accept (the decisions log, 5 Nov).
	if crewTeamMember("team-test", "member") || call("member", "GET", base+"/files", "", "").Code != 403 {
		t.Fatal("an invited person must have no access before accepting")
	}
	if w = call("outsider", "POST", base+"/invite", `{"accept":true}`, ""); w.Code != 404 {
		t.Fatalf("someone not invited accepted: %d %s", w.Code, w.Body.String())
	}
	for _, who := range []string{"member", "teammate"} {
		if w = call(who, "POST", base+"/invite", `{"accept":true}`, ""); w.Code != 200 {
			t.Fatalf("%s accept: %d %s", who, w.Code, w.Body.String())
		}
	}
	agents := []macSharedAgent{{Tab: "team01", Audience: "team"}, {Tab: "chosen01", Audience: "chosen", People: []string{"member", "outsider"}}, {Tab: "all01", Audience: "everyone"}}
	for _, tc := range []struct{ tab, who, reason string }{{"team01", "member", ""}, {"chosen01", "member", ""}, {"chosen01", "outsider", "not_in_audience"}, {"all01", "outsider", ""}} {
		if _, reason := macSharedAudience(agents, tc.tab, tc.who, "owner", crewTeamPeople("team-test")); reason != tc.reason {
			t.Fatalf("audience %s/%s: %s", tc.tab, tc.who, reason)
		}
	}
	if !crewTeamMember("team-test", "member") {
		t.Fatal("active member missing")
	}
	for _, who := range []string{"member", "outsider"} {
		if w = call(who, "PUT", base, config, ""); w.Code != 403 {
			t.Fatalf("%s changed team: %d %s", who, w.Code, w.Body.String())
		}
	}
	file := base + "/file?path=brief.md"
	w = call("owner", "PUT", file, "first", "first")
	if w.Code != 200 {
		t.Fatalf("first write: %d %s", w.Code, w.Body.String())
	}
	state, e := readCrewTeamFileState("team-test")
	if e != nil {
		t.Fatal(e)
	}
	first := state.Versions["brief.md"].File.Hash
	w = call("owner", "PUT", file+"&base="+first, "owner changed", "owner-change")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = call("member", "PUT", file+"&base="+first, "member changed", "member-change")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var receipt crewTeamReceipt
	if e = json.Unmarshal(w.Body.Bytes(), &receipt); e != nil {
		t.Fatal(e)
	}
	if len(receipt.Conflicts) != 1 {
		t.Fatalf("no recorded clash: %+v", receipt)
	}
	copyPath := receipt.Conflicts[0]
	w = call("member", "GET", base+"/history?revision=2&path="+url.QueryEscape(copyPath), "", "")
	if w.Code != 200 || w.Body.String() != "owner changed" {
		t.Fatalf("historical losing bytes: %d %s", w.Code, w.Body.String())
	}
	// Lost response replay must not change the folder revision or create another copy.
	w = call("member", "PUT", file+"&base="+first, "member changed", "member-change")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var retry crewTeamReceipt
	_ = json.Unmarshal(w.Body.Bytes(), &retry)
	if retry.Revision != receipt.Revision {
		t.Fatal("retry became another edit")
	}
	// A clash keeps the losing bytes in history, not another file in the folder.
	state, e = readCrewTeamFileState("team-test")
	if e != nil || len(state.manifest()) != 1 {
		t.Fatalf("visible copy was created: %+v %v", state, e)
	}
	w = call("member", "GET", base+"/clashes", "", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"losing_revision":2`) {
		t.Fatal(w.Code, w.Body.String())
	}
	w = call("member", "POST", base+"/resolve", `{"path":"brief.md","keep":"revision:2","winning_revision":3,"transfer_id":"restore-old"}`, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = call("member", "GET", file, "", "")
	if w.Code != 200 || w.Body.String() != "owner changed" {
		t.Fatal(w.Code, w.Body.String())
	}
	w = call("member", "GET", base+"/clashes", "", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"clashes":[]`) {
		t.Fatal(w.Code, w.Body.String())
	}
	w = call("member", "PUT", file, "member changed", "member-again")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	// Every joined member edits the shared folder; there is no per-person switch (owner, 5 Nov).
	if w = call("teammate", "PUT", base+"/file?path=teammate.md", "teammate wrote", "teammate-write"); w.Code != 200 {
		t.Fatalf("joined teammate could not write the shared folder: %d %s", w.Code, w.Body.String())
	}
	for _, who := range []string{"outsider"} {
		for _, method := range []string{"GET", "PUT"} {
			w = call(who, method, file, "should not be saved", "denied")
			if w.Code != 403 || !strings.Contains(w.Body.String(), "team_folder_forbidden") {
				t.Fatalf("%s %s: %d %s", who, method, w.Code, w.Body.String())
			}
		}
	}
	for _, p := range []string{"../outside.txt", ".env", "_system/crew-data/owner/chats.json", ".claude/projects/chat.jsonl"} {
		w = call("member", "PUT", base+"/file?path="+url.QueryEscape(p), "private", "invalid")
		if w.Code != 403 || !strings.Contains(w.Body.String(), "team_path_forbidden") {
			t.Fatalf("path %s: %d %s", p, w.Code, w.Body.String())
		}
	}
	if _, err := api.crewSyncRoots(t.Context(), &UserClaims{UserID: "member"}, "team-test"); err == nil {
		t.Fatal("folder member acquired whole Crew sync authority")
	}
	// Reject a payload before a private-state path or native archive can be imported.
	invalid := `{"action":"prepare","client":"mac","transfer_id":"bad","snapshot":{"files":{},"payloads":{"state":{"size":1,"hash":"` + strings.Repeat("a", 64) + `"}}}}`
	w = call("member", "POST", base+"/sync", invalid, "")
	if w.Code != 409 {
		t.Fatalf("state payload accepted: %d %s", w.Code, w.Body.String())
	}
	// Two actual clients, real HTTP bytes and private Git bundles, no mocked
	// content transport. The interrupted download edits a local file in flight.
	var onDownload atomic.Bool
	var interruptUpload atomic.Bool
	var lastDownload atomic.Value
	var dirtyPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		who := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/sync") {
			raw, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(raw))
			var tr crewTeamTransfer
			if json.Unmarshal(raw, &tr) == nil && who == "member" && tr.Action == "pull" {
				lastDownload.Store(tr.TransferID)
			}
		}
		if r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/sync/") && interruptUpload.CompareAndSwap(true, false) {
			raw, _ := io.ReadAll(io.LimitReader(r.Body, r.ContentLength/2))
			r.Body = io.NopCloser(bytes.NewReader(raw))
		}

		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/sync/") && onDownload.CompareAndSwap(true, false) {
			_ = os.WriteFile(dirtyPath, []byte("member edited while downloading"), 0600)
		}
		r = r.WithContext(context.WithValue(r.Context(), UserContextKey, &UserClaims{UserID: who, Username: who}))
		router.ServeHTTP(w, r)
	}))
	defer server.Close()
	newClient := func(who string) (desktopConnection, string, *crewTeamLocal) {
		dir := t.TempDir()
		root := filepath.Join(dir, "files")
		if err := os.MkdirAll(root, 0700); err != nil {
			t.Fatal(err)
		}
		c := desktopConnection{ID: who, Server: server.URL, Token: who, User: UserInfo{ID: who}}
		team, _ := readCrewTeam("team-test")
		b := &crewTeamLocal{FolderID: team.Folder.ID, Connection: who, Project: "team-test", RemoteUser: who, Client: who + "-mac", Root: root, LocalBase: map[string]crewsync.File{}, RemoteBase: map[string]crewsync.File{}}
		return c, dir, b
	}
	c1, d1, b1 := newClient("owner")
	c2, d2, b2 := newClient("member")
	sync := func(c desktopConnection, dir string, b *crewTeamLocal) {
		t.Helper()
		if err := syncCrewTeamLocal(t.Context(), c.User.ID, c, dir, b); err != nil {
			t.Fatal("sync", c.User.ID, err)
		}
	}
	read := func(root, path, want string) {
		t.Helper()
		got, err := os.ReadFile(filepath.Join(root, path))
		if err != nil || string(got) != want {
			t.Fatalf("file %s: %q %v; want %q", path, got, err, want)
		}
	}
	if err := os.WriteFile(filepath.Join(b1.Root, "sync.md"), []byte("owner sends"), 0600); err != nil {
		t.Fatal(err)
	}
	interruptUpload.Store(true)
	if err := syncCrewTeamLocal(t.Context(), "owner", c1, d1, b1); err == nil {
		t.Fatal("interrupted upload unexpectedly completed")
	}
	if _, err := os.Stat(filepath.Join(d1, "upload", "pending.json")); err != nil {
		t.Fatal("interrupted upload lost its pending descriptor", err)
	}
	sync(c1, d1, b1)
	sync(c2, d2, b2)
	read(b2.Root, "sync.md", "owner sends")
	_ = os.WriteFile(filepath.Join(b2.Root, "sync.md"), []byte("member returns"), 0600)
	sync(c2, d2, b2)
	sync(c1, d1, b1)
	read(b1.Root, "sync.md", "member returns")
	_ = os.WriteFile(filepath.Join(b1.Root, "sync.md"), []byte("server arriving"), 0600)
	sync(c1, d1, b1)
	dirtyPath = filepath.Join(b2.Root, "sync.md")
	onDownload.Store(true)
	sync(c2, d2, b2)
	read(b2.Root, "sync.md", "member edited while downloading")
	sync(c2, d2, b2)
	sync(c1, d1, b1)
	read(b1.Root, "sync.md", "member edited while downloading")
	// Already accepted bytes must not be replayed as a new write.
	beforeRetry, _ := readCrewTeamFileState("team-test")
	sync(c2, d2, b2)
	afterRetry, _ := readCrewTeamFileState("team-test")
	if beforeRetry.Revision != afterRetry.Revision {
		t.Fatal("unchanged client created another revision")
	}
	fmt.Println("[CREW_TEAM_PROOF] two HTTP/Git clients: interrupted upload resumes, owner to member, member to owner, local edit during download retained, unchanged retry passed")
	// PR932: directory/file replacements must not strand the next sync after deleting children.
	if err := os.MkdirAll(filepath.Join(b1.Root, "reports"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(b1.Root, "reports", "old.md"), []byte("old report"), 0600); err != nil {
		t.Fatal(err)
	}
	sync(c1, d1, b1)
	sync(c2, d2, b2)
	if err := os.RemoveAll(filepath.Join(b1.Root, "reports")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(b1.Root, "reports"), []byte("now a file"), 0600); err != nil {
		t.Fatal(err)
	}
	sync(c1, d1, b1)
	sync(c2, d2, b2)
	read(b2.Root, "reports", "now a file")
	if err := os.Remove(filepath.Join(b2.Root, "reports")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(b2.Root, "reports"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(b2.Root, "reports", "new.md"), []byte("directory again"), 0600); err != nil {
		t.Fatal(err)
	}
	sync(c2, d2, b2)
	sync(c1, d1, b1)
	read(b1.Root, "reports/new.md", "directory again")
	// Idle rounds create neither bundles nor manifests; completed metadata is bounded per client/direction.
	for i := 0; i < 4; i++ {
		sync(c2, d2, b2)
	}
	contentDir, _ := crewTeamContentDir("team-test")
	transfers := filepath.Join(contentDir, "transfers", crewTransferDigest("member\x00"+b2.Client), "download")
	metadata, _ := filepath.Glob(filepath.Join(transfers, "*", "snapshot.json"))
	payloads, _ := filepath.Glob(filepath.Join(transfers, "*", "content"))
	if len(metadata) > 2 || len(payloads) != 0 {
		t.Fatalf("completed downloads retained metadata=%d payloads=%d", len(metadata), len(payloads))
	}
	t.Log("[CREW_TEAM_PROOF] idle_transfer_retention=bounded directory_file_transitions=both_directions")
	// Canonical reads reject symlinks and hardlinks even when a host process plants one.
	dir, _ := crewTeamContentDir("team-test")
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("not shared"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, link := range []struct {
		name string
		fn   func(string, string) error
	}{{"link.txt", os.Symlink}, {"hard.txt", os.Link}} {
		if err := link.fn(outside, filepath.Join(dir, "files", link.name)); err != nil {
			t.Fatal(err)
		}
		w = call("member", "GET", base+"/file?path="+link.name, "", "")
		if w.Code == 200 || strings.Contains(w.Body.String(), "not shared") {
			t.Fatalf("link read escaped: %d %s", w.Code, w.Body.String())
		}
	}
	// Revocation follows an allowed read, and also blocks replay of already accepted edits.
	w = call("member", "GET", file, "", "")
	if w.Code != 200 || w.Body.String() != "member changed" {
		t.Fatal(w.Code, w.Body.String())
	}
	w = call("owner", "PUT", base, `{"people":[],"folder":{"path":"docs"}}`, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if id, _ := lastDownload.Load().(string); id != "" {
		w = call("member", "GET", base+"/sync/"+id+"?client=member-mac&direction=download", "", "")
		if w.Code != 403 {
			t.Fatal("revoked member read old staged bytes", w.Code, w.Body.String())
		}
	} else {
		t.Fatal("no completed member download to test revocation")
	}
	if crewTeamMember("team-test", "member") {
		t.Fatal("revoked member remained in the audience")
	}
	for _, method := range []string{"GET", "PUT"} {
		w = call("member", method, file, "member changed", "member-change")
		if w.Code != 403 {
			t.Fatal("revoked operation", method, w.Code, w.Body.String())
		}
	}
	fmt.Println("[CREW_TEAM_PROOF] allowed owner/member writes, history-only clash, retry receipt, outside-folder/private-state/link refusals, owner-only configuration and revocation passed")
	// Changing the selected folder retains the old source copy/history privately,
	// and an old client's upload is refused even if it still has Team membership.
	oldTeam, _ := readCrewTeam("team-test")
	oldDir, _ := crewTeamContentDir("team-test")
	w = call("owner", "PUT", base, `{"name":"Example","on":true,"people":[{"user_id":"member"}],"folder":{"path":"other"}}`, "")
	if w.Code != 200 {
		t.Fatal("change folder", w.Code, w.Body.String())
	}
	// Removed earlier, so added back as an invite.
	if w = call("member", "POST", base+"/invite", `{"accept":true}`, ""); w.Code != 200 {
		t.Fatal("member accept again", w.Code, w.Body.String())
	}
	newTeam, _ := readCrewTeam("team-test")
	if newTeam.Folder.ID == oldTeam.Folder.ID {
		t.Fatal("folder identity reused")
	}
	if _, err := os.Stat(filepath.Join(oldDir, "history")); err != nil {
		t.Fatal("old history not kept", err)
	}
	w = call("owner", "PUT", file+"&folder_id="+oldTeam.Folder.ID, "stale old folder bytes", "old-folder-write")
	if w.Code != 403 {
		t.Fatal("stale folder write admitted", w.Code)
	}
	if err := syncCrewTeamLocal(t.Context(), c1.User.ID, c1, d1, b1); err == nil {
		t.Fatal("old local binding admitted")
	}
	w = call("member", "GET", base+"/files", "", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"files":[]`) {
		t.Fatal("old files leaked into newly selected folder", w.Code, w.Body.String())
	}
	t.Log("[CREW_TEAM_PROOF] folder_change=isolated stale_write=403 old_history=retained")
	// Live Showcase first-upload failure: there is no canonical files directory yet.
	c3, d3, b3 := newClient("owner")
	if err := os.WriteFile(filepath.Join(b3.Root, "first-upload.md"), []byte("new folder starts here"), 0600); err != nil {
		t.Fatal(err)
	}
	sync(c3, d3, b3)
	w = call("member", "GET", base+"/file?path=first-upload.md", "", "")
	if w.Code != 200 || w.Body.String() != "new folder starts here" {
		t.Fatal("first upload into empty generation", w.Code, w.Body.String())
	}
	t.Log("[CREW_TEAM_PROOF] first_upload_into_empty_folder=pass")

}
