package server

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

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
		"docs/a.md": {Size: 14, Version: crewTeamFileVersion{Hash: strings.Repeat("a", 64), Size: 14}}}}
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
	put := func(content string) crewTeamFileVersion {
		t.Helper()
		v, err := crewTeamChunkReader(strings.NewReader(content), int64(len(content)), false, func(id string, chunk []byte) error { return crewTeamPutChunk(store, id, chunk) })
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
	put := func(content string) crewTeamFileVersion {
		t.Helper()
		v, err := crewTeamChunkReader(strings.NewReader(content), int64(len(content)), false, func(id string, chunk []byte) error { return crewTeamPutChunk(store, id, chunk) })
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
			if err = crewTeamAssemble(store, crewTeamFileVersion{Chunks: current.Chunks}, &stored); err != nil || stored.String() != tc.want || current.Author != "member" {
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
