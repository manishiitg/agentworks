package agentworksclient

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A resumed delta cannot splice a rewritten prefix, partial line or escaped file.
func TestNativeJournalDeltaResumesOnlyExactCompleteRecords(t *testing.T) {
	for _, provider := range []string{"claude", "codex"} {
		t.Run(provider, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
			a := AgentAttachment{Provider: provider, SessionID: "11111111-2222-4333-8444-555555555555"}
			dir := filepath.Join(home, ".claude", "projects", "one")
			name := a.SessionID + ".jsonl"
			if provider == "codex" {
				dir = filepath.Join(home, ".codex", "sessions", "2026", "11", "15")
				name = "rollout-now-" + a.SessionID + ".jsonl"
			}
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, name)
			first := "{\"type\":\"user\"}\n"
			second := "{\"type\":\"assistant\"}\n"
			if err := os.WriteFile(path, []byte(first+second[:8]), 0600); err != nil {
				t.Fatal(err)
			}
			got, hash, _, err := ReadNativeJournalDelta(a, 0, "")
			if err != nil || string(got) != first {
				t.Fatalf("partial record advanced cursor: %q %v", got, err)
			}
			if err = os.WriteFile(path, []byte(first+second), 0600); err != nil {
				t.Fatal(err)
			}
			got, _, _, err = ReadNativeJournalDelta(a, int64(len(first)), hash)
			if err != nil || string(got) != second {
				t.Fatalf("resume: %q %v", got, err)
			}
			if _, _, _, err = ReadNativeJournalDelta(a, 1, hash); err == nil {
				t.Fatal("mid-record cursor accepted")
			}
			if err = os.WriteFile(path, []byte(strings.Replace(first, "user", "evil", 1)+second), 0600); err != nil {
				t.Fatal(err)
			}
			if _, _, _, err = ReadNativeJournalDelta(a, int64(len(first)), hash); err == nil {
				t.Fatal("rewritten prefix accepted")
			}
			outside := filepath.Join(t.TempDir(), "private")
			if err = os.WriteFile(outside, []byte(first), 0600); err != nil {
				t.Fatal(err)
			}
			if err = os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err = os.Symlink(outside, path); err != nil {
				t.Fatal(err)
			}
			if _, _, _, err = ReadNativeJournalDelta(a, 0, ""); err == nil {
				t.Fatal("symlink journal accepted")
			}
		})
	}
}

// The commit is accepted but its reply is lost: the next hook uses server ACK,
// not the old local cursor, and sends only subsequently appended JSONL bytes.
func TestNativeOfflineCopyLostReceiptDoesNotReuploadHistory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	a := AgentAttachment{ID: "lease", AgentName: "worker", Provider: "claude", SessionID: "11111111-2222-4333-8444-555555555555"}
	dir := filepath.Join(home, ".claude", "projects", "one")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, a.SessionID+".jsonl")
	first := "{\"type\":\"user\"}\n"
	second := "{\"type\":\"assistant\"}\n"
	if err := os.WriteFile(path, []byte(first), 0600); err != nil {
		t.Fatal(err)
	}
	cur := NativeCopyCursor{Project: "project", Agent: "agent"}
	commits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/external/v1/agents/attachment" {
			var args map[string]string
			_ = json.NewDecoder(r.Body).Decode(&args)
			if args["attachment_id"] != "lease" || args["action"] != "offline_copy_status" {
				t.Error("identity changed")
			}
			_ = json.NewEncoder(w).Encode(struct {
				NativeCopyCursor
				Shared bool `json:"shared"`
			}{cur, true})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/plan") {
			fmt.Fprint(w, `{"missing":[]}`)
			return
		}
		if !strings.HasSuffix(r.URL.Path, "/commit") {
			t.Errorf("unexpected route %s", r.URL.Path)
			w.WriteHeader(400)
			return
		}
		var body struct {
			Offset     int64  `json:"offset"`
			Attachment string `json:"attachment"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Offset != cur.Offset || body.Attachment != a.ID {
			t.Error("cursor or attachment changed")
		}
		commits++
		if commits == 1 {
			cur.Offset = int64(len(first))
		} else {
			cur.Offset += int64(len(second))
		}
		cur.Revision++
		data := []byte(first)
		if commits > 1 {
			data = []byte(first + second)
		}
		sum := sha256.Sum256(data)
		cur.TailHash = hex.EncodeToString(sum[:])
		if commits == 1 {
			w.WriteHeader(503)
			fmt.Fprint(w, `{"error":"lost receipt"}`)
			return
		}
		_ = json.NewEncoder(w).Encode(cur)
	}))
	defer srv.Close()
	client, err := New(srv.URL, "fixture-token")
	if err != nil {
		t.Fatal(err)
	}
	old := NativeCopyCursor{}
	if _, err = client.SyncNativeCopy(context.Background(), a, old); err == nil {
		t.Fatal("unacknowledged commit reported success")
	}
	if err = os.WriteFile(path, []byte(first+second), 0600); err != nil {
		t.Fatal(err)
	}
	next, err := client.SyncNativeCopy(context.Background(), a, old)
	if err != nil || next.Offset != int64(len(first+second)) || commits != 2 {
		t.Fatalf("resume failed: %+v %v commits=%d", next, err, commits)
	}
	// #3579: the server's copy was reset to a lower revision. The local cursor
	// is dropped for the server's instead of refusing every later turn.
	cur.Revision = 0
	reset, err := client.SyncNativeCopy(context.Background(), a, next)
	if err != nil || reset.Revision != 0 || reset.Offset != next.Offset || commits != 2 {
		t.Fatalf("server reset was not trusted: %+v %v commits=%d", reset, err, commits)
	}
}

// A turn-end hook only schedules the upload; it cannot wait on a server or
// schedule work for a different process/lease.
func TestNativeCopyTurnEndNeverBlocksHook(t *testing.T) {
	started, release := make(chan struct{}, 1), make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		<-release
		fmt.Fprint(w, `{"shared":false}`)
	}))
	defer srv.Close()
	defer close(release)
	client, err := New(srv.URL, "fixture-token")
	if err != nil {
		t.Fatal(err)
	}
	s := &localSession{HostKey: "process", client: client, Attachment: AgentAttachment{ID: "lease", Provider: "claude", SessionID: "exact"}}
	b := &localBridge{root: t.TempDir(), sessions: map[string]*localSession{localBridgeKey("claude", "exact"): s}, changed: make(chan struct{})}
	in := LocalBridgeRequest{Action: "observe", Provider: "claude", SessionID: "exact", HostKey: "wrong", AttachmentID: "lease", SyncTranscript: true}
	b.handle(t.Context(), t.Context(), in)
	select {
	case <-started:
		t.Fatal("wrong process scheduled upload")
	default:
	}
	in.HostKey = "process"
	in.AttachmentID = "old"
	b.handle(t.Context(), t.Context(), in)
	select {
	case <-started:
		t.Fatal("old lease scheduled upload")
	default:
	}
	in.AttachmentID = "lease"
	returned := make(chan struct{})
	go func() { b.handle(t.Context(), t.Context(), in); close(returned) }()
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("hook blocked on upload")
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("turn end did not schedule upload")
	}
	release <- struct{}{}
	b.workers.Wait()
}
