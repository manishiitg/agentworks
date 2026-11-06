package server

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
)

// Showcase, 6 Nov (PLAT-856-509): adding and removing a shared folder gave the Team a new, empty store each time, so
// every copy uploaded all 7,606 files again and 7,571 false clashes were notified. A folder change now keeps the store:
// only the new folder's files go up, nothing clashes, a removed folder's files leave what people can read, and sharing
// it again brings them back without an upload. A folder inside one already shared is refused, not saved as no change.
func TestCrewTeamFolderChangeKeepsStoreWithoutReupload(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	t.Setenv("MULTI_USER_MODE", "true")
	crewTeamTestDocs(t, "fold-test", "fold-test")
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","can_create":true,"products":["work"]},{"id":"member","username":"member","can_create":true,"products":["work"]}]}`)
	if err := defaultProjectOwners().Register(projectOwnerRecord{Product: "work", Folder: "fold-test", OwnerID: "owner", ProjectID: "fold-test"}); err != nil {
		t.Fatal(err)
	}
	if err := writeCrewTeam(crewTeam{ProjectID: "fold-test", Name: "Example", OwnerID: "owner", On: true, People: []crewTeamPerson{{UserID: "member"}}, Folder: crewTeamFolder{ID: "f1", Folders: []string{"team"}}}); err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{}
	router := mux.NewRouter()
	api.registerCrewTeamRoutes(router.PathPrefix("/api").Subrouter())
	put := func(user, folders string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest("PUT", "/api/crew-teams/fold-test", strings.NewReader(`{"name":"Example","on":true,"people":[{"user_id":"member"}],"folder":{"folders":[`+folders+`]}}`))
		r = r.WithContext(context.WithValue(r.Context(), UserContextKey, &UserClaims{UserID: user, Username: user}))
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	type copyOf struct {
		c   desktopConnection
		dir string
		b   *crewTeamLocal
		ctx context.Context
	}
	newCopy := func(who string, files map[string]string) copyOf {
		dir := t.TempDir()
		root := filepath.Join(dir, "files")
		for rel, content := range files {
			if err := os.MkdirAll(filepath.Dir(filepath.Join(root, rel)), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, rel), []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.MkdirAll(root, 0o700); err != nil {
			t.Fatal(err)
		}
		claims := &UserClaims{UserID: who, Username: who}
		return copyOf{c: desktopConnection{ID: who, Server: crewTeamServerHost, User: UserInfo{ID: who}}, dir: dir,
			b:   &crewTeamLocal{FolderID: "f1", Connection: who, Project: "fold-test", RemoteUser: who, Client: who + "-copy", Root: root},
			ctx: context.WithValue(t.Context(), crewTeamTransportKey{}, crewTeamInProcess{handler: api.crewTeamServerHandler(), claims: claims})}
	}
	sync := func(cs ...copyOf) {
		t.Helper()
		for _, c := range cs {
			if err := syncCrewTeamLocal(c.ctx, c.c.User.ID, c.c, c.dir, c.b); err != nil {
				t.Fatal("sync", c.c.User.ID, err)
			}
		}
	}
	state := func() crewTeamFileState {
		s, _ := readCrewTeamFileState("fold-test")
		return s
	}
	owner := newCopy("owner", map[string]string{"team/a.md": "a", "team/b.md": "b", "code/c.md": "c"})
	member := newCopy("member", nil)
	sync(owner, owner, member) // the owner's first round receives, the second sends team/
	start := state()
	if len(start.held(crewTeamFolder{Folders: []string{"team"}})) != 2 {
		t.Fatalf("team/ did not go up: %+v", start.Versions)
	}
	// Adding code/ keeps the store; only code/c.md goes up.
	if w := put("owner", `"code","team"`); w.Code != 200 {
		t.Fatal("add folder", w.Code, w.Body.String())
	}
	if team, _ := readCrewTeam("fold-test"); team.Folder.ID != "f1" {
		t.Fatalf("a folder change started a new store: %s", team.Folder.ID)
	}
	sync(owner, member)
	added := state()
	if paths, _ := crewTeamChangedSince(added, start.Revision, 100); len(paths) != 1 || paths[0] != "code/c.md" {
		t.Fatalf("adding a folder sent %v, want only code/c.md", paths)
	}
	if got, _ := os.ReadFile(filepath.Join(member.b.Root, "code", "c.md")); string(got) != "c" {
		t.Fatalf("member did not receive the added folder: %q", got)
	}
	// A folder inside one already shared is refused in plain words, with no save.
	if w := put("member", `"code","team","team/sub"`); w.Code != 400 || !strings.Contains(w.Body.String(), "team/ already shares that folder.") {
		t.Fatalf("nested folder: %d %s", w.Code, w.Body.String())
	}
	// Removing code/ keeps team/ in step and sends nothing; code/ leaves what people can read.
	if w := put("owner", `"team"`); w.Code != 200 {
		t.Fatal("remove folder", w.Code, w.Body.String())
	}
	sync(owner, member)
	if s := state(); s.Revision != added.Revision {
		t.Fatalf("removing a folder made copies send files: revision %d -> %d", added.Revision, s.Revision)
	}
	if _, err := os.Stat(filepath.Join(owner.b.Root, "code", "c.md")); err != nil {
		t.Fatal("the owner's own files of a folder no longer shared were touched", err)
	}
	// Shared again: the store's files are announced once and adopted, with no upload.
	if w := put("owner", `"code","team"`); w.Code != 200 {
		t.Fatal("share again", w.Code, w.Body.String())
	}
	announced := state()
	sync(owner, member, owner)
	if s := state(); s.Revision != announced.Revision || announced.Revision != added.Revision+1 {
		t.Fatalf("sharing a folder again uploaded files: revisions %d, %d, %d", added.Revision, announced.Revision, s.Revision)
	}
	if s := state(); len(s.Clashes) != 0 {
		t.Fatalf("folder changes made clashes: %+v", s.Clashes)
	}
}

// Showcase, 6 Nov: 5,982 of the false clashes had the same person on both sides (two of their own copies) and most had
// the same content on both sides. Neither is a clash; the later version wins silently.
func TestCrewTeamSameContentOrSamePersonIsNoClash(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	if err := writeCrewTeam(crewTeam{ProjectID: "same-test", OwnerID: "owner", On: true, Folder: crewTeamFolder{ID: "f1", Whole: true}}); err != nil {
		t.Fatal(err)
	}
	store, err := crewTeamChunkStore("same-test")
	if err != nil {
		t.Fatal(err)
	}
	put := func(content string) chunkedFile {
		t.Helper()
		v, err := chunkReader(strings.NewReader(content), int64(len(content)), false, func(id string, chunk []byte) error { return putChunk(store, id, chunk) })
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	commit := func(actor, transfer string, c crewTeamChange) crewTeamReceipt {
		t.Helper()
		receipt, err := commitCrewTeamChanges(t.Context(), "same-test", actor, transfer, crewTransferDigest(transfer), []crewTeamChange{c})
		if err != nil {
			t.Fatal(err)
		}
		return receipt
	}
	same, first, second := put("same bytes"), put("member copy one"), put("member copy two")
	sameOnMac := same
	sameOnMac.Exec = true // the same bytes from a copy whose disk marks it executable
	commit("owner", "o/1", crewTeamChange{Path: "a.md", After: &same})
	if r := commit("member", "m/1", crewTeamChange{Path: "a.md", After: &sameOnMac}); len(r.Conflicts) != 0 {
		t.Fatalf("same content from another person clashed: %v", r.Conflicts)
	}
	commit("member", "m/2", crewTeamChange{Path: "b.md", After: &first})
	if r := commit("member", "m2/1", crewTeamChange{Path: "b.md", After: &second}); len(r.Conflicts) != 0 {
		t.Fatalf("the same person's two copies clashed: %v", r.Conflicts)
	}
	s, _ := readCrewTeamFileState("same-test")
	if len(s.Clashes) != 0 || s.Versions["b.md"].File.Hash != second.Hash {
		t.Fatalf("clashes %+v, b.md %+v", s.Clashes, s.Versions["b.md"])
	}
}

// Showcase, 6 Nov: one message per clashing file (7,571). A person now hears once per sync round, with a count.
func TestCrewTeamClashNoticeOncePerPersonPerRound(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	if err := writeCrewTeam(crewTeam{ProjectID: "round-test", Name: "Example", OwnerID: "owner", On: true, Folder: crewTeamFolder{ID: "f1", Whole: true}}); err != nil {
		t.Fatal(err)
	}
	store, err := crewTeamChunkStore("round-test")
	if err != nil {
		t.Fatal(err)
	}
	put := func(content string) *chunkedFile {
		t.Helper()
		v, err := chunkReader(strings.NewReader(content), int64(len(content)), false, func(id string, chunk []byte) error { return putChunk(store, id, chunk) })
		if err != nil {
			t.Fatal(err)
		}
		return &v
	}
	commit := func(actor, transfer string, changes ...crewTeamChange) {
		t.Helper()
		if _, err := commitCrewTeamChanges(t.Context(), "round-test", actor, transfer, crewTransferDigest(transfer), changes); err != nil {
			t.Fatal(err)
		}
	}
	drain := func() (jobs []notifyJob) {
		for {
			select {
			case job := <-notifyQueue:
				if job.event == "team_clash" {
					jobs = append(jobs, job)
				}
			default:
				return jobs
			}
		}
	}
	paths := []string{"a.bin", "b.bin", "c.bin", "d.bin"}
	for _, p := range paths {
		commit("owner", "owner-mac/"+p, crewTeamChange{Path: p, After: put("owner " + p)})
	}
	drain()
	// The member's copy sends four files from an older base in two batches of one round.
	commit("member", "member-mac/t1", crewTeamChange{Path: "a.bin", After: put("member a")}, crewTeamChange{Path: "b.bin", After: put("member b")}, crewTeamChange{Path: "c.bin", After: put("member c")})
	commit("member", "member-mac/t2", crewTeamChange{Path: "d.bin", After: put("member d")})
	jobs := drain()
	if len(jobs) != 1 || len(jobs[0].direct) != 2 {
		t.Fatalf("want one notice to each person for the round, got %+v", jobs)
	}
	for _, d := range jobs[0].direct {
		if !strings.Contains(d.msg.Body, "3 files changed by both you and") {
			t.Fatalf("summary for %s: %q", d.user, d.msg.Body)
		}
	}
}

// Showcase, 6 Nov: the false clashes already recorded are closed once at startup, after a backup of the record.
func TestCrewTeamStartupClearsFalseClashes(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	if err := writeCrewTeam(crewTeam{ProjectID: "clear-test", OwnerID: "owner", On: true, Folder: crewTeamFolder{ID: "f1", Whole: true}}); err != nil {
		t.Fatal(err)
	}
	dir, err := crewTeamContentDir("clear-test")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	file := func(hash string) *chunkedFile {
		return &chunkedFile{Hash: strings.Repeat(hash, 64), Size: 1}
	}
	event := func(path string, rev uint64, v *chunkedFile) crewTeamEvent {
		return crewTeamEvent{Path: path, Kind: "sent", Revision: rev, File: v.file(), Chunks: v.Chunks}
	}
	state := crewTeamFileState{Revision: 6, Versions: map[string]crewTeamVersion{}, Receipts: map[string]crewTeamReceipt{},
		Events: []crewTeamEvent{event("same.md", 1, file("a")), event("same.md", 2, file("a")), event("real.md", 3, file("b")), event("real.md", 4, file("c")), event("own.md", 5, file("d")), event("own.md", 6, file("e"))},
		Clashes: []crewTeamClash{
			{Path: "same.md", LosingRevision: 1, WinningRevision: 2, LosingAuthor: "owner", WinningAuthor: "member"},
			{Path: "real.md", LosingRevision: 3, WinningRevision: 4, LosingAuthor: "owner", WinningAuthor: "member"},
			{Path: "own.md", LosingRevision: 5, WinningRevision: 6, LosingAuthor: "member", WinningAuthor: "member"},
		}}
	if err = fsutil.WriteJSONAtomic(filepath.Join(dir, "files.json"), state, 0o600); err != nil {
		t.Fatal(err)
	}
	clearFalseCrewTeamClashes(t.Context())
	after, _ := readCrewTeamFileState("clear-test")
	if open := crewTeamOpenClashPaths(after.Clashes); len(open) != 1 || open[0] != "real.md" {
		t.Fatalf("open clashes after cleanup: %v", open)
	}
	if backups, _ := filepath.Glob(filepath.Join(dir, "files.json.before-clash-cleanup-*")); len(backups) != 1 {
		t.Fatalf("no backup of the record: %v", backups)
	}
}
