package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/chunks"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/coding-agent-loop/workspace/publication"
)

// PLAT-856-509 (Team sync audit, 5 Nov): one file over the 64 MiB per-file limit made every commit fail with
// "invalid shared file metadata", so the copy stopped syncing both ways; it is now left out and named. A case-only
// rename (docs -> Docs) is sent as a removal of the old name before the new name, so no batch holds both spellings.
func TestCrewTeamScanLeavesOutOversizeFileAndSendsRemovalsFirst(t *testing.T) {
	root := t.TempDir()
	for path, content := range map[string]string{"a.md": "small", "Docs/a.md": "renamed folder"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, path)), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, path), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	big, err := os.Create(filepath.Join(root, "big.bin"))
	if err == nil {
		err = big.Truncate(publication.MaxFileBytes + 1)
		big.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	// The copy last had docs/a.md, before the folder was renamed to Docs.
	b := &crewTeamLocal{Root: root, Downloaded: true, Files: map[string]crewTeamLocalFile{
		"docs/a.md": {Version: chunkedFile{Hash: strings.Repeat("a", 64), Size: 14}}}}
	scan, err := crewTeamScan(t.Context(), b, crewTeamFolder{ID: "f1", Whole: true})
	if err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(scan.changes, func(c crewTeamChange) bool { return c.Path == "big.bin" }) || !slices.Equal(b.TooLarge, []string{"big.bin"}) {
		t.Fatalf("oversize file: changes %+v too_large %v", scan.changes, b.TooLarge)
	}
	if len(scan.changes) != 3 || scan.changes[0].Path != "docs/a.md" || scan.changes[0].After != nil {
		t.Fatalf("the old spelling's removal is not sent first: %+v", scan.changes)
	}
}

// crewTeamChunkServer answers a copy's chunk requests from memory, for a test of the receiving side.
type crewTeamChunkServer struct {
	chunks map[string][]byte
	served map[string]int
	calls  int
	onCall func(call int)
}

func (s *crewTeamChunkServer) RoundTrip(req *http.Request) (*http.Response, error) {
	s.calls++
	if s.onCall != nil {
		s.onCall(s.calls)
	}
	var in struct {
		Chunks []transferChunk `json:"chunks"`
	}
	if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
		return nil, err
	}
	var body bytes.Buffer
	for _, c := range in.Chunks {
		s.served[c.ID]++
		if err := writeChunkFrame(&body, c.ID, s.chunks[c.ID]); err != nil {
			return nil, err
		}
	}
	return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(&body), Request: req}, nil
}

// PLAT-856-509 (Showcase, 6 Nov): a 2,000-file first download ran past its round's deadline, wrote nothing and started
// again for ever. A page that runs out of time keeps the batches it wrote, and the next round goes on from there.
func TestCrewTeamDownloadPageLargerThanARoundCompletesOverRounds(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	root, dir := t.TempDir(), t.TempDir()
	server := &crewTeamChunkServer{chunks: map[string][]byte{}, served: map[string]int{}}
	const count = crewTeamDownloadBatch*2 + 50
	files := make([]crewTeamRemoteFile, 0, count)
	for i := range count {
		data := []byte(fmt.Sprintf("file %d", i))
		v, err := chunkReader(bytes.NewReader(data), int64(len(data)), false, func(id string, piece []byte) error {
			server.chunks[id] = append([]byte(nil), piece...)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, crewTeamRemoteFile{Path: fmt.Sprintf("docs/%04d.md", i), Version: &v})
	}
	b := &crewTeamLocal{Root: root, Project: "page-test", Client: "copy", Files: map[string]crewTeamLocalFile{}}
	round, stop := context.WithCancel(context.WithValue(t.Context(), crewTeamTransportKey{}, http.RoundTripper(server)))
	defer stop()
	// The deadline passes while the second batch is on its way.
	server.onCall = func(call int) {
		if call == 2 {
			stop()
		}
	}
	c := desktopConnection{ID: "server", Server: "http://team.example.com"}
	if _, err := crewTeamDownload(round, "owner", c, "/api/crew-teams/page-test", "", dir, b, files); err == nil {
		t.Fatal("the round did not run out of time")
	}
	if len(b.Files) < crewTeamDownloadBatch || len(b.Files) >= count {
		t.Fatalf("after the first round %d files of %d are kept", len(b.Files), count)
	}
	saved, err := readCrewTeamLocal(dir)
	if err != nil || len(saved.Files) < crewTeamDownloadBatch {
		t.Fatalf("the finished batch was not saved: %d %v", len(saved.Files), err)
	}
	kept := len(b.Files)
	server.onCall = nil
	next := context.WithValue(t.Context(), crewTeamTransportKey{}, http.RoundTripper(server))
	if _, err = crewTeamDownload(next, "owner", c, "/api/crew-teams/page-test", "", dir, b, files); err != nil {
		t.Fatal(err)
	}
	if len(b.Files) != count {
		t.Fatalf("%d files of %d", len(b.Files), count)
	}
	for i, f := range files[:kept] {
		if server.served[f.Version.Chunks[0].ID] != 1 {
			t.Fatalf("file %d was fetched %d times; the next round must go on, not start again", i, server.served[f.Version.Chunks[0].ID])
		}
	}
	if data, err := os.ReadFile(filepath.Join(root, "docs", fmt.Sprintf("%04d.md", count-1))); err != nil || string(data) != fmt.Sprintf("file %d", count-1) {
		t.Fatalf("last file: %q %v", data, err)
	}
}

// PLAT-856-509 (Showcase, 6 Nov): a chat turn leaves a copy's first download to the background loop, and syncs only a
// copy that has its files, or makes a copy that does not exist yet.
func TestCrewTeamTurnSkipsSyncOnlyDuringAFirstDownload(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	if err := writeCrewTeam(crewTeam{ProjectID: "first-download", OwnerID: "owner", On: true, Folder: crewTeamFolder{ID: "f1", Whole: true}}); err != nil {
		t.Fatal(err)
	}
	if crewTeamFirstDownloadPending("member", "first-download") {
		t.Fatal("a copy that does not exist yet must be made by the turn")
	}
	dir, err := crewTeamServerCopyDir("member", "first-download", "f1")
	if err == nil {
		err = os.MkdirAll(dir, 0o700)
	}
	if err == nil {
		err = saveCrewTeamLocal(dir, crewTeamLocal{Project: "first-download"})
	}
	if err != nil {
		t.Fatal(err)
	}
	if !crewTeamFirstDownloadPending("member", "first-download") {
		t.Fatal("a turn did not leave the first download alone")
	}
	if err = saveCrewTeamLocal(dir, crewTeamLocal{Project: "first-download", Downloaded: true}); err != nil {
		t.Fatal(err)
	}
	if crewTeamFirstDownloadPending("member", "first-download") {
		t.Fatal("a finished copy must sync on a turn")
	}
}

// PLAT-856-509 (owner, 6 Nov): a Mac Crew's dashboards reach the server in their own read copy. Only dashboard files and
// the database copy are taken, the copy is swapped whole, and reads go there, never to the Crew's own db/, which an
// always-on agent may be writing.
func TestCrewMacDashboardsReadCopy(t *testing.T) {
	version := func(store string, content string) chunks.File {
		t.Helper()
		v, err := chunks.Split(strings.NewReader(content), int64(len(content)), false, func(id string, data []byte) error { return putChunk(store, id, data) })
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	store, root := t.TempDir(), t.TempDir()
	page, data := version(store, "<html>first</html>"), version(store, "database copy")
	for name, files := range map[string]map[string]chunks.File{
		"nothing":           {},
		"a chat":            {"builder/conversation/chat.json": page},
		"a credential":      {"db/reports/.env": page},
		"outside":           {"../db/reports/index.html": page},
		"the Crew settings": {"product.json": page},
	} {
		if validateCrewMacDashboards(files) == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
	first := map[string]chunks.File{"db/reports/index.html": page, crewMacDashboardsDatabase: data}
	if err := validateCrewMacDashboards(first); err != nil {
		t.Fatal(err)
	}
	if err := swapCrewMacDashboards(root, store, first); err != nil {
		t.Fatal(err)
	}
	// A swap that cannot finish leaves the copy as it was.
	broken := map[string]chunks.File{"db/reports/index.html": {Hash: page.Hash, Size: 1, Chunks: []chunks.Chunk{{ID: strings.Repeat("d", 64), Len: 1}}}}
	if swapCrewMacDashboards(root, store, broken) == nil {
		t.Fatal("a missing chunk was accepted")
	}
	got, err := os.ReadFile(filepath.Join(root, crewroot.MacDashboardsFolder, "db", "reports", "index.html"))
	if err != nil || string(got) != "<html>first</html>" {
		t.Fatalf("read copy: %q %v", got, err)
	}
	if crewroot.SyncContentAllowed("resources/"+crewroot.MacDashboardsFolder+"/db/reports/index.html", false) {
		t.Fatal("Team sync would carry the read copy")
	}
	second := map[string]chunks.File{"db/reports/other.html": version(store, "<html>second</html>")}
	if err = swapCrewMacDashboards(root, store, second); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(root, crewroot.MacDashboardsFolder, "db", "reports", "index.html")); !os.IsNotExist(err) {
		t.Fatalf("a removed dashboard stayed in the copy: %v", err)
	}
}

// PLAT-856-509 (Team sync lighter, 6 Nov): a watched round reads only the folders that changed, yet still sends an
// edit, a removal and a new folder's file; a folder nothing changed in is not read.
func TestCrewTeamWatchedRoundReadsOnlyChangedFolders(t *testing.T) {
	root := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, rel)), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, rel), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, rel := range []string{"a/edit.md", "a/same.md", "b/gone.md", "quiet/1.md", "quiet/2.md"} {
		write(rel, "first "+rel)
	}
	b := &crewTeamLocal{Root: root, Downloaded: true, Files: map[string]crewTeamLocalFile{}}
	share := crewTeamFolder{ID: "f1", Whole: true}
	full, err := crewTeamScan(t.Context(), b, share)
	if err != nil || len(full.changes) != 5 {
		t.Fatalf("first scan: %v %+v", err, full.changes)
	}
	for rel, entry := range full.entries { // as if committed
		b.Files[rel] = entry
	}
	write("a/edit.md", "a longer second version")
	write("c/new.md", "new")
	if err = os.Remove(filepath.Join(root, "b", "gone.md")); err != nil {
		t.Fatal(err)
	}
	scan, err := crewTeamScanChanged(t.Context(), b, share, map[string]bool{"a/edit.md": false, "b/gone.md": true, "c": true}, false)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range scan.changes {
		got = append(got, c.Path)
	}
	if !slices.Equal(got, []string{"b/gone.md", "a/edit.md", "c/new.md"}) || scan.changes[0].After != nil {
		t.Fatalf("changes %v", got)
	}
	if scan.stated != 3 { // a/edit.md, a/same.md, c/new.md; quiet/ is not read
		t.Fatalf("stated %d files", scan.stated)
	}
}

// PLAT-856-552: settle a racy first read once, then warm scans read no
// contents, while equal-size edits/replacements preserving mtime still sync.
func TestCrewTeamStableStampReusesContentAndDetectsPreservedMtime(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("this OS has no strong identity in FileInfo; content is conservatively rechecked")
	}
	root := t.TempDir()
	p := filepath.Join(root, "edit.md")
	old := time.Now().Add(-time.Minute)
	write := func(path, data string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	write(p, "first")
	b := &crewTeamLocal{Root: root, Downloaded: true, Files: map[string]crewTeamLocalFile{}}
	share := crewTeamFolder{ID: "f1", Whole: true}
	scan := func() crewTeamScanResult {
		t.Helper()
		got, err := crewTeamScan(t.Context(), b, share)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	first := scan()
	if first.readFiles != 1 || len(first.changes) != 1 {
		t.Fatalf("first capture: %+v", first)
	}
	for rel, entry := range first.entries {
		b.Files[rel] = entry // upload acknowledged
	}
	// The first stamp's ctime lies in the read's own second. After the clock
	// settles, a successful recheck records a trustworthy read-start time.
	time.Sleep(time.Until(time.Now().Truncate(time.Second).Add(time.Second)) + 10*time.Millisecond)
	settled := scan()
	for rel, entry := range settled.entries {
		b.Files[rel] = entry
	}
	if warm := scan(); warm.readFiles != 0 || warm.reused != 1 || len(warm.changes) != 0 {
		t.Fatalf("warm unchanged file was reread: %+v", warm)
	}
	write(p, "other")
	edited := scan()
	if edited.readFiles != 1 || len(edited.changes) != 1 || edited.changes[0].After.Hash == b.Files["edit.md"].Version.Hash {
		t.Fatalf("same-size/mtime edit was lost: %+v", edited)
	}
	for rel, entry := range edited.entries {
		b.Files[rel] = entry
	}
	replacement := filepath.Join(t.TempDir(), "replacement")
	write(replacement, "third")
	if err := os.Rename(replacement, p); err != nil {
		t.Fatal(err)
	}
	if replaced := scan(); replaced.readFiles != 1 || len(replaced.changes) != 1 {
		t.Fatalf("atomic replacement with preserved size/mtime was lost: %+v", replaced)
	}
}

// PLAT-856-509: a delete sent from a stale base used to win over a teammate's newer edit (the clash was recorded but
// the file removed). The edit now stays and is recorded again, so the deleter's copy receives it back.
func TestCrewTeamStaleDeleteKeepsTheNewerEdit(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	if err := writeCrewTeam(crewTeam{ProjectID: "clash-test", OwnerID: "owner", On: true, Folder: crewTeamFolder{ID: "f1", Whole: true}}); err != nil {
		t.Fatal(err)
	}
	store, err := crewTeamChunkStore("clash-test")
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
	first, edit := put("first"), put("teammate's newer edit")
	for i, c := range []crewTeamChange{{Path: "notes.md", After: &first}, {Path: "notes.md", Before: first.Hash, After: &edit}} {
		transfer := "owner-" + string(rune('a'+i))
		if _, err = commitCrewTeamChanges(t.Context(), "clash-test", "owner", transfer, crewTransferDigest(transfer), []crewTeamChange{c}); err != nil {
			t.Fatal(err)
		}
	}
	receipt, err := commitCrewTeamChanges(t.Context(), "clash-test", "member", "stale-delete", crewTransferDigest("stale-delete"), []crewTeamChange{{Path: "notes.md", Before: first.Hash}})
	if err != nil {
		t.Fatal(err)
	}
	state, _ := readCrewTeamFileState("clash-test")
	if v := state.Versions["notes.md"]; v.File == nil || v.File.Hash != edit.Hash || !slices.Contains(receipt.Conflicts, "notes.md") {
		t.Fatalf("the stale delete won: %+v conflicts %v", v, receipt.Conflicts)
	}
	root, _ := crewTeamFilesRoot("clash-test")
	if got, _ := os.ReadFile(filepath.Join(root, "notes.md")); string(got) != "teammate's newer edit" {
		t.Fatalf("the team's file: %q", got)
	}
	if paths, _ := crewTeamChangedSince(state, receipt.Revision-1, 100); !slices.Contains(paths, "notes.md") {
		t.Fatal("the kept edit is not sent back to the deleter")
	}
}

// PLAT-856-509: the member-copy migration used os.CopyFS, which refuses links, and a crash after the copy left
// "target_not_empty" for good. Links now copy as links, and a finished copy whose record still points at the old
// folder is resumed.
func TestCrewTeamMemberCopyMigrationCopiesLinksAndResumes(t *testing.T) {
	state := t.TempDir()
	t.Setenv("AGENTWORKS_STATE_ROOT", state)
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	crewTeamTestDocs(t, "mig-test", "mig-test")
	if err := defaultProjectOwners().Register(projectOwnerRecord{Product: "work", Folder: "mig-test", OwnerID: "owner", ProjectID: "mig-test"}); err != nil {
		t.Fatal(err)
	}
	record := func(member string) (string, string, *crewTeamLocal) {
		t.Helper()
		dir := filepath.Join(state, "team-working", "server", member, "copy")
		old := filepath.Join(dir, "files")
		if err := os.MkdirAll(old, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(old, "a.md"), []byte("member file"), 0o600); err != nil {
			t.Fatal(err)
		}
		b := &crewTeamLocal{Project: "mig-test", RemoteUser: member, Root: old}
		if err := saveCrewTeamLocal(dir, *b); err != nil {
			t.Fatal(err)
		}
		return dir, old, b
	}
	dir, old, b := record("member")
	if err := os.Symlink("a.md", filepath.Join(old, "link.md")); err != nil {
		t.Fatal(err)
	}
	if result, _ := migrateCrewTeamMemberCopy(dir, old, "stamp", b); result != "moved" {
		t.Fatalf("a copy with a link: %s", result)
	}
	if link, err := os.Readlink(filepath.Join(b.Root, "link.md")); err != nil || link != "a.md" {
		t.Fatalf("the link was not copied as a link: %q %v", link, err)
	}
	// A crash after the finished copy was renamed into place, before the record was saved.
	dir, old, b = record("member2")
	target, err := crewTeamMemberCopyRoot("mig-test", "member2")
	if err != nil {
		t.Fatal(err)
	}
	if err = copyCrewTeamTree(old, target); err == nil {
		err = os.WriteFile(filepath.Join(target, crewTeamMigratedMarker), nil, 0o600)
	}
	if err != nil {
		t.Fatal(err)
	}
	if result, _ := migrateCrewTeamMemberCopy(dir, old, "stamp", b); result != "moved_resumed" || b.Root != target {
		t.Fatalf("the interrupted migration did not resume: %s %s", result, b.Root)
	}
	if _, err = os.Stat(filepath.Join(target, crewTeamMigratedMarker)); !os.IsNotExist(err) {
		t.Fatal("the migration marker stayed in the member's copy")
	}
}

// PLAT-856-509: after a server restart nothing was cached for a member's Mac that is off, so a message to its agent
// was refused as "This agent isn't shared with you." The last list read from that Mac is kept on disk.
func TestCrewTeamMemberMacAgentKnownAfterRestartWhileMacOff(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","products":["work"]},{"id":"member","username":"member","products":["work"]}]}`)
	if err := defaultProjectOwners().Register(projectOwnerRecord{Product: "work", Folder: "mac-team", OwnerID: "owner", ProjectID: "mac-team"}); err != nil {
		t.Fatal(err)
	}
	if err := crewroot.WriteSync("mac-team", crewroot.SyncBinding{Mode: "relay", PairID: "owner-pair", Side: "server", Editing: "local", PeerProjectID: "mac-crew"}); err != nil {
		t.Fatal(err)
	}
	if err := writeCrewTeam(crewTeam{ProjectID: "mac-team", OwnerID: "owner", On: true, People: []crewTeamPerson{{UserID: "member", Where: "mac", MacPair: "member-pair"}}}); err != nil {
		t.Fatal(err)
	}
	tab := crewMacTeamTab{Person: "member", Tab: macMainTab, ConversationID: "member-main", Name: "Main"}
	saveCrewMacTabs("mac-team", "member", []crewMacTeamTab{tab})
	crewMacTeamTabsCache.Delete("mac-team\x00member") // a restart: nothing in memory
	key := crewMacTabRow("mac-team", tab).ConversationKey
	if person, got, ok := crewMemberMacAgentFor(t.Context(), "mac-team", key, "owner"); !ok || person != "member" || got.Tab != macMainTab {
		t.Fatalf("the member's agent is unknown while their Mac is off: %q %+v %t", person, got, ok)
	}
}

// PLAT-856-509: a Mac agent's answer was awaited only in memory, so a restart lost it. The wait is recorded and
// resumed at startup; the answer reaches the original inbox and the record is cleared.
func TestMacAgentAnswerWaitResumesAfterRestart(t *testing.T) {
	f, _ := newExternalCrewCommandFixture(t)
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	store := agentMessageStore{Conversations: []agentConversation{{ID: "inbox-1", Next: 1, Endpoints: [2]agentMessageEndpoint{
		{UserID: "other", Kind: triggerCallerUser, ID: "other", External: true, NamedAgentID: "agent-other"},
		{UserID: "owner", Kind: triggerCallerCrew, Profile: "work", ID: "team", ChatKey: "team"},
	}, Messages: []agentMessage{{ID: "ask-1", From: 0, Sequence: 1, Text: "answer this", SentAt: time.Now(), Delivery: "delivered"}}}}}
	if err := saveAgentMessageStore(t.Context(), store); err != nil {
		t.Fatal(err)
	}
	previous := macAgentAnswerReadForTest
	t.Cleanup(func() { macAgentAnswerReadForTest = previous })
	macAgentAnswerReadForTest = func(_ *StreamingAPI, w macAgentAwaiting, _, _ int) (map[string]any, int) {
		return map[string]any{"working": false, "messages": []any{map[string]any{"role": "assistant", "text": "answer after restart"}}}, 200
	}
	// What the previous process recorded before it stopped.
	if err := updateMacAgentAwaiting(func([]macAgentAwaiting) []macAgentAwaiting {
		return []macAgentAwaiting{{Inbox: "inbox-1", Submission: "ask-1", From: 1, Project: "team", Tab: macMainTab, Mac: "owner", SenderID: "other", Started: time.Now().Add(-time.Minute)}}
	}); err != nil {
		t.Fatal(err)
	}
	f.api.resumeMacAgentAnswers()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	for {
		s, _ := readAgentMessageStore(ctx)
		answered := len(s.Conversations) == 1 && slices.ContainsFunc(s.Conversations[0].Messages, func(m agentMessage) bool { return m.Text == "answer after restart" && m.From == 1 })
		waiting, _ := readMacAgentAwaiting()
		if answered && len(waiting) == 0 {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("the resumed wait did not deliver the answer: answered=%t waiting=%d", answered, len(waiting))
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// crewTeamTestDocs gives a test its own workspace documents with this Crew's folders, so a member's copy folder
// (team-copies/<member> in the Crew's folder) resolves on any machine, not only where a workspace-docs folder exists.
func crewTeamTestDocs(t *testing.T, folder, project string) {
	t.Helper()
	docs := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	private, err := crewroot.PrivateRoot(docs, project)
	if err == nil {
		err = os.MkdirAll(private, 0o700)
	}
	if err == nil {
		err = os.MkdirAll(filepath.Join(docs, "Crew", folder), 0o700)
	}
	if err != nil {
		t.Fatal(err)
	}
}

// Owner, 5 Nov: two people editing different lines of one shared file both keep their edits. A change sent from an
// older base is merged by lines into a new revision every copy (the sender's included) downloads; the same line
// edited by both takes the later side and is a clash; a binary file keeps the whole-file rule (later wins).
func TestCrewTeamLineMergeOfConcurrentEdits(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	if err := writeCrewTeam(crewTeam{ProjectID: "merge-test", OwnerID: "owner", On: true, Folder: crewTeamFolder{ID: "f1", Whole: true}}); err != nil {
		t.Fatal(err)
	}
	store, err := crewTeamChunkStore("merge-test")
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
	for _, tc := range []struct {
		name, base, ours, theirs, want string
		hunks                          int // overlapping parts; -1 = not merged (whole-file rule)
	}{
		{"different lines", "title\nalpha\nbeta\ngamma\ndelta\nend\n", "title\nALPHA by owner\nbeta\ngamma\ndelta\nend\n",
			"title\nalpha\nbeta\ngamma\nDELTA by member\nend\nnew last line\n", "title\nALPHA by owner\nbeta\ngamma\nDELTA by member\nend\nnew last line\n", 0},
		{"same line", "title\nalpha\nbeta\ngamma\ndelta\nend\n", "title\nalpha owner\nbeta\ngamma\ndelta\nend\n",
			"title\nalpha member\nbeta\ngamma\ndelta\nmember end\n", "title\nalpha member\nbeta\ngamma\ndelta\nmember end\n", 1},
		{"binary", "title\x00base\nend\n", "title\x00base\nowner end\n", "title\x00member\nend\n", "title\x00member\nend\n", -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := strings.ReplaceAll(tc.name, " ", "-") + ".txt"
			first, ours, theirs := put(tc.base), put(tc.ours), put(tc.theirs)
			for i, c := range []crewTeamChange{{Path: path, After: &first}, {Path: path, Before: first.Hash, After: &ours}} {
				transfer := path + "-owner-" + string(rune('a'+i))
				if _, err := commitCrewTeamChanges(t.Context(), "merge-test", "owner", transfer, crewTransferDigest(transfer), []crewTeamChange{c}); err != nil {
					t.Fatal(err)
				}
			}
			receipt, err := commitCrewTeamChanges(t.Context(), "merge-test", "member", path+"-member", crewTransferDigest(path+"-member"), []crewTeamChange{{Path: path, Before: first.Hash, After: &theirs}})
			if err != nil {
				t.Fatal(err)
			}
			root, _ := crewTeamFilesRoot("merge-test")
			if got, _ := os.ReadFile(filepath.Join(root, path)); string(got) != tc.want {
				t.Fatalf("team file %q, want %q", got, tc.want)
			}
			state, _ := readCrewTeamFileState("merge-test")
			current := state.Versions[path]
			var stored strings.Builder
			if err = assembleChunked(store, chunkedFile{Chunks: current.Chunks}, &stored); err != nil || stored.String() != tc.want || current.Author != "member" {
				t.Fatalf("stored version %q by %s, err %v", stored.String(), current.Author, err)
			}
			if clash := slices.Contains(receipt.Conflicts, path); clash != (tc.hunks != 0) {
				t.Fatalf("conflicts %v for %d overlapping parts", receipt.Conflicts, tc.hunks)
			}
			if tc.hunks != 0 {
				last := state.Clashes[len(state.Clashes)-1]
				if last.Path != path || last.WinningAuthor != "member" || last.Merged != (tc.hunks > 0) || tc.hunks > 0 && last.Hunks != tc.hunks {
					t.Fatalf("clash record %+v", last)
				}
				if v := state.versionAt(path, last.LosingRevision); v == nil || v.Hash != ours.Hash {
					t.Fatal("the owner's version is not kept in history")
				}
			}
			// Both copies converge: the new revision goes to every copy, including the member's that sent its own.
			if paths, _ := crewTeamChangedSince(state, receipt.Revision-1, 100); !slices.Contains(paths, path) {
				t.Fatalf("the merged revision is not sent out: %v", paths)
			}
			if tc.hunks == 0 && (current.File.Hash == theirs.Hash || current.File.Hash == ours.Hash) {
				t.Fatal("the merge did not produce a new version for both copies to download")
			}
		})
	}
}

// Owner, 5 Nov: a same-line clash tells both people once, stays on the sync line, and an agent sorts it out: clashes
// returns both versions and resolve keep=other puts the other person's version back as a new revision.
func TestCrewTeamClashNotifiesBothAndAgentResolves(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	t.Setenv("MULTI_USER_MODE", "true")
	crewTeamTestDocs(t, "clash-test", "clash-test")
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","can_create":true,"products":["work"]},{"id":"member","username":"member","can_create":true,"products":["work"]},{"id":"outsider","username":"outsider","can_create":true,"products":["work"]}]}`)
	if err := defaultProjectOwners().Register(projectOwnerRecord{Product: "work", Folder: "clash-test", OwnerID: "owner", ProjectID: "clash-test"}); err != nil {
		t.Fatal(err)
	}
	team := crewTeam{ProjectID: "clash-test", Name: "Example", OwnerID: "owner", On: true, People: []crewTeamPerson{{UserID: "member"}}, Folder: crewTeamFolder{ID: "f1", Whole: true}}
	if err := writeCrewTeam(team); err != nil {
		t.Fatal(err)
	}
	store, err := crewTeamChunkStore("clash-test")
	if err != nil {
		t.Fatal(err)
	}
	put := func(content string) chunkedFile {
		v, err := chunkReader(strings.NewReader(content), int64(len(content)), false, func(id string, chunk []byte) error { return putChunk(store, id, chunk) })
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	commit := func(actor, transfer string, c crewTeamChange) {
		if _, err := commitCrewTeamChanges(t.Context(), "clash-test", actor, transfer, crewTransferDigest(transfer), []crewTeamChange{c}); err != nil {
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
	drain()
	const ownerText, memberText = "title\nalpha owner\nend\n", "title\nalpha member\nend\n"
	base, ours, theirs := put("title\nalpha\nend\n"), put(ownerText), put(memberText)
	commit("owner", "a", crewTeamChange{Path: "notes.txt", After: &base})
	commit("owner", "b", crewTeamChange{Path: "notes.txt", Before: base.Hash, After: &ours})
	commit("member", "c", crewTeamChange{Path: "notes.txt", Before: base.Hash, After: &theirs})
	other := put("other\n")
	commit("member", "d", crewTeamChange{Path: "other.txt", After: &other}) // a later round: no second notice
	jobs := drain()
	if len(jobs) != 1 || len(jobs[0].direct) != 2 {
		t.Fatalf("want one notice to both people, got %+v", jobs)
	}
	bodies := map[string]string{}
	for _, d := range jobs[0].direct {
		bodies[d.user] = d.msg.Body
	}
	if !strings.Contains(bodies["owner"], "You and member both changed notes.txt. member's version is in the file; yours is kept.") || !strings.Contains(bodies["member"], "Your version is in the file; owner's is kept.") {
		t.Fatalf("notice texts %q", bodies)
	}
	if sync := crewTeamTabSyncFor(team, "owner"); sync == nil || !strings.Contains(sync.Line, "1 clash to sort out: notes.txt") {
		t.Fatalf("sync line %+v", sync)
	}
	call := func(user string, args map[string]any) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/api/external/call", nil)
		r = r.WithContext(context.WithValue(r.Context(), UserContextKey, &UserClaims{UserID: user, Username: user}))
		w := httptest.NewRecorder()
		(&StreamingAPI{}).externalCrewTeamSyncCall(w, r, args)
		return w
	}
	w := call("member", map[string]any{"crew_id": "clash-test", "action": "clashes"})
	var listed struct {
		Clashes []crewTeamClashView `json:"clashes"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &listed) != nil || len(listed.Clashes) != 1 {
		t.Fatalf("clashes: %d %s", w.Code, w.Body.String())
	}
	c := listed.Clashes[0]
	if c.Current == nil || c.Other == nil || c.Current.Text != memberText || c.Other.Text != ownerText || c.Other.Who != "owner" {
		t.Fatalf("clash versions %+v", c)
	}
	if w = call("outsider", map[string]any{"crew_id": "clash-test", "action": "resolve", "path": "notes.txt", "clash_id": c.ID, "keep": "other"}); w.Code != 403 {
		t.Fatalf("an outsider resolved a clash: %d", w.Code)
	}
	if w = call("member", map[string]any{"crew_id": "clash-test", "action": "resolve", "path": "notes.txt", "clash_id": c.ID, "keep": "other"}); w.Code != 200 {
		t.Fatalf("resolve: %d %s", w.Code, w.Body.String())
	}
	state, _ := readCrewTeamFileState("clash-test")
	root, _ := crewTeamFilesRoot("clash-test")
	if got, _ := os.ReadFile(filepath.Join(root, "notes.txt")); string(got) != ownerText || state.Versions["notes.txt"].Author != "member" || state.Versions["notes.txt"].Revision != state.Revision {
		t.Fatalf("keep other wrote %q as %+v", got, state.Versions["notes.txt"])
	}
	if open := crewTeamOpenClashPaths(state.Clashes); len(open) != 0 {
		t.Fatalf("clash still open: %v", open)
	}
	if sync := crewTeamTabSyncFor(team, "owner"); !strings.Contains(sync.Line, "no clashes") {
		t.Fatalf("sync line after resolve %q", sync.Line)
	}
}
