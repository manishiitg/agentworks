package agentworksclient

import (
	"context"
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

// Lost native/ACK receipts may not acknowledge stdout, an assistant echo or
// another session. Only a persisted, matching USER journal record closes it.
func TestNativeHandbackRequiresDurableOwnUserRecordAndRetriesAck(t *testing.T) {
	for _, provider := range []string{"claude", "codex"} {
		t.Run(provider, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
			a := AgentAttachment{ID: "lease", AgentID: "named", Provider: provider, SessionID: "11111111-2222-4333-8444-555555555555"}
			dir := filepath.Join(home, ".claude", "projects", "one")
			name := a.SessionID + ".jsonl"
			if provider == "codex" {
				dir = filepath.Join(home, ".codex", "sessions", "2026", "11", "15")
				name = "rollout-now-" + a.SessionID + ".jsonl"
			}
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			journal := filepath.Join(dir, name)
			if err := os.WriteFile(journal, []byte("{}\n"), 0600); err != nil {
				t.Fatal(err)
			}
			acks, offers := 0, 0
			acknowledged := false
			returnedText := strings.Repeat("\n", (96<<10)/3+1)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				_ = json.NewDecoder(r.Body).Decode(&body)
				if body["provider"] != a.Provider || body["session"] != a.SessionID || body["attachment"] != a.ID {
					t.Error("wrong source tuple")
				}
				switch r.URL.Path {
				case "/api/crew-teams/crew/agents/agent/offline-copy/handback":
					if acknowledged {
						fmt.Fprint(w, `{"records":[],"text":""}`)
					} else {
						_ = json.NewEncoder(w).Encode(map[string]any{"records": []map[string]string{{"id": "return-1"}}, "text": returnedText})
					}
				case "/api/crew-teams/crew/agents/agent/offline-copy/handback-ack":
					acks++
					if acks == 1 {
						w.WriteHeader(503)
						fmt.Fprint(w, `{"error":"lost ACK receipt"}`)
						return
					}
					acknowledged = true
					fmt.Fprint(w, `{}`)
				default:
					t.Error("fabricated mailbox route")
					w.WriteHeader(404)
				}
			}))
			defer srv.Close()
			client, err := New(srv.URL, "fixture-token")
			if err != nil {
				t.Fatal(err)
			}
			s := &localSession{Attachment: a, NativeRemote: "native", client: client}
			key := localBridgeKey(provider, a.SessionID)
			b := &localBridge{root: t.TempDir(), sessions: map[string]*localSession{key: s}, changed: make(chan struct{}), options: LocalBridgeOptions{NativeUserOffer: func(_ context.Context, _ string, got AgentAttachment, text string) error {
				if got.ID != a.ID {
					t.Error("replacement got user content")
				}
				offers++
				return nil
			}}}
			cur := NativeCopyCursor{Project: "crew", Agent: "agent"}
			// Newline-heavy source fits the input cap but exceeds the quoted cap.
			if err = b.processNativeHandback(t.Context(), s, a, client, cur); err == nil || offers != 0 || s.NativeHandback != nil {
				t.Fatal("oversized quote was offered or persisted")
			}
			returnedText = "> Question: status?\n> Answer: ready"
			if err = b.processNativeHandback(t.Context(), s, a, client, cur); err != nil {
				t.Fatal(err)
			}
			if offers != 1 || acks != 0 || s.NativeHandback == nil {
				t.Fatal("transport offer treated as durable receipt")
			}
			// Simulate helper restart with its saved private pending record.
			saved, err := json.Marshal(s.NativeHandback)
			if err != nil {
				t.Fatal(err)
			}
			s.NativeHandback = nil
			if err = json.Unmarshal(saved, &s.NativeHandback); err != nil {
				t.Fatal(err)
			}
			text := s.NativeHandback.Text
			appendRecord := func(role string) {
				t.Helper()
				var record any
				if provider == "claude" {
					record = map[string]any{"type": role, "message": map[string]any{"role": role, "content": text}}
				} else {
					record = map[string]any{"type": "response_item", "payload": map[string]any{"type": "message", "role": role, "content": []any{map[string]string{"type": "input_text", "text": text}}}}
				}
				raw, _ := json.Marshal(record)
				f, e := os.OpenFile(journal, os.O_APPEND|os.O_WRONLY, 0600)
				if e != nil {
					t.Fatal(e)
				}
				_, e = f.Write(append(raw, '\n'))
				_ = f.Close()
				if e != nil {
					t.Fatal(e)
				}
			}
			appendRecord("assistant")
			if err = b.processNativeHandback(t.Context(), s, a, client, cur); err != nil {
				t.Fatal(err)
			}
			if acks != 0 || offers != 1 {
				t.Fatal("assistant echo acknowledged or duplicate injection")
			}
			// #3579: an offer the chat never recorded used to stay Attempted forever.
			s.NativeHandback.AttemptedAt = time.Now().Add(-time.Hour)
			if err = b.processNativeHandback(t.Context(), s, a, client, cur); err != nil {
				t.Fatal(err)
			}
			if acks != 0 || offers != 2 {
				t.Fatalf("unrecorded offer was not offered again after its wait: offers=%d", offers)
			}
			if err = b.processNativeHandback(t.Context(), s, a, client, cur); err != nil || offers != 2 {
				t.Fatal("re-offer repeated within its wait")
			}
			// The app stores the user record without the offered text's trailing newline.
			text = strings.TrimSuffix(text, "\n")
			other := a
			other.SessionID = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
			if err = b.processNativeHandback(t.Context(), s, other, client, cur); err == nil {
				t.Fatal("wrong session inherited pending records")
			}
			appendRecord("user")
			if err = b.processNativeHandback(t.Context(), s, a, client, cur); err == nil {
				t.Fatal("lost ACK not retained")
			}
			if offers != 2 || acks != 1 || !s.NativeHandback.Confirmed {
				t.Fatal("durable receipt not saved before remote ACK")
			}
			if err = b.processNativeHandback(t.Context(), s, a, client, cur); err != nil {
				t.Fatal(err)
			}
			if offers != 2 || acks != 2 || s.NativeHandback != nil {
				t.Fatal("ACK retry reinjected or lost records")
			}
		})
	}
}
