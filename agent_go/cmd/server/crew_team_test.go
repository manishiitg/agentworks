package server

import (
	"context"
	"encoding/json"
	"fmt"
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
	t.Setenv("LOCAL_MODE", "false")
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
		r = r.WithContext(context.WithValue(r.Context(), UserContextKey, &UserClaims{UserID: user, Username: user}))
		r.Header.Set("X-Team-Transfer", transfer)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	base := "/api/crew-teams/team-test"
	config := `{"name":"Example","people":[{"user_id":"member","can_edit_shared_folder":true},{"user_id":"teammate","can_edit_shared_folder":false}],"folder":{"path":"docs"}}`
	w := call("owner", "PUT", base, config, "")
	if w.Code != 200 {
		t.Fatalf("owner config: %d %s", w.Code, w.Body.String())
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
	for _, who := range []string{"teammate", "outsider"} {
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
	var dirtyPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		who := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
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
		b := &crewTeamLocal{Connection: who, Project: "team-test", RemoteUser: who, Client: who + "-mac", Root: root, LocalBase: map[string]crewsync.File{}, RemoteBase: map[string]crewsync.File{}}
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
	fmt.Println("[CREW_TEAM_PROOF] two HTTP/Git clients: owner to member, member to owner, local edit during download retained, unchanged retry passed")
	// Canonical reads reject symlinks and hardlinks even when a host process plants one.
	dir, _ := crewTeamDir("team-test")
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
}
