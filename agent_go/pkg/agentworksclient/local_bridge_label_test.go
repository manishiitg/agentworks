package agentworksclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Labels reuse the current lease and cannot block commands while HTTP waits.
func TestBridgeLabelRefreshKeepsLeaseAndDoesNotBlock(t *testing.T) {
	started, release := make(chan map[string]string, 1), make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var args map[string]string
		_ = json.NewDecoder(r.Body).Decode(&args)
		started <- args
		<-release
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	defer close(release)
	client, err := New(srv.URL, "fixture-token")
	if err != nil {
		t.Fatal(err)
	}
	s := &localSession{Attachment: AgentAttachment{ID: "lease", AgentID: "stable-id", AgentName: "worker", Label: "Earlier", Provider: "codex", SessionID: "session"}, client: client,
		ReadPosition: 9}
	b := &localBridge{root: t.TempDir(), sessions: map[string]*localSession{"chat": s}, changed: make(chan struct{}), options: LocalBridgeOptions{
		ClientBuild: "current-build",
		ChatLabel:   func(AgentAttachment, string) string { return "Renamed" },
		ChatContext: func(context.Context, AgentAttachment, string) (string, string) { return "project", "feature/mail" },
	}}
	done := make(chan struct{})
	go func() { b.refreshChatLabel(context.Background(), s); close(done) }()
	select {
	case args := <-started:
		if _, supplied := args["delivery_route"]; !supplied {
			t.Fatal("unknown route must clear any previous adapter")
		}
		if args["action"] != "label" || args["attachment_id"] != "lease" || args["agent_name"] != "worker" || args["delivery_route"] != "" || args["repository"] != "project" || args["branch"] != "feature/mail" || args["client_build"] != "current-build" {
			t.Fatal(args)
		}
	case <-time.After(time.Second):
		t.Fatal("label update did not start")
	}
	if !b.lockWithin(100 * time.Millisecond) {
		t.Fatal("label HTTP held the global bridge lock")
	}
	b.mu.Unlock()
	release <- struct{}{}
	<-done
	if s.Attachment.ID != "lease" || s.Attachment.AgentID != "stable-id" || s.Attachment.Label != "Renamed" || s.Attachment.ClientBuild != "current-build" || s.ReadPosition != 9 {
		t.Fatal("label update changed delivery state")
	}
	// A new helper reports its build even when all chat metadata is unchanged.
	b.options.ClientBuild = "updated-build"
	done = make(chan struct{})
	go func() { b.refreshChatLabel(context.Background(), s); close(done) }()
	select {
	case args := <-started:
		if args["client_build"] != "updated-build" || args["attachment_id"] != "lease" {
			t.Fatal(args)
		}
	case <-time.After(time.Second):
		t.Fatal("unchanged chat kept the previous helper build")
	}
	release <- struct{}{}
	<-done
	if s.Attachment.ClientBuild != "updated-build" || s.ReadPosition != 9 {
		t.Fatal("helper update changed message state")
	}
	// An unchanged label and a closed chat cause no HTTP request.
	b.refreshChatLabel(context.Background(), s)
	s.ServerClosed = true
	s.Attachment.Label = "Earlier"
	b.refreshChatLabel(context.Background(), s)
	select {
	case <-started:
		t.Fatal("unneeded label request")
	default:
	}
	// PLAT-856-373: an extra network has no separate native socket. Its
	// metadata must describe the existing chat adapter without project names.
	primary := &localSession{NativeRemote: "desktop+unix:///example.sock", Attachment: AgentAttachment{Provider: "codex", SessionID: "joined-session"}}
	joined := &localSession{Also: true, client: client, Attachment: AgentAttachment{ID: "joined-lease", AgentID: "joined-id", AgentName: "joined-worker", Provider: "codex", SessionID: "joined-session", Repository: "private-project", Branch: "private-branch"}}
	b.sessions[localBridgeKey("codex", "joined-session")] = primary
	b.sessions["joined"] = joined
	for _, tc := range []struct {
		native, hook string
		offline      bool
		want         string
	}{
		{native: "desktop+unix:///example.sock", want: "codex_desktop"},
		{hook: "codex_hook", want: "codex_hook"},
		{native: "desktop+unix:///example.sock", offline: true, want: "inbox"},
	} {
		primary.NativeRemote, primary.HookTransport, primary.Offline = tc.native, tc.hook, tc.offline
		done = make(chan struct{})
		go func() { b.refreshChatLabel(context.Background(), joined); close(done) }()
		select {
		case args := <-started:
			if args["delivery_route"] != tc.want || args["attachment_id"] != "joined-lease" || args["repository"] != "" || args["branch"] != "" {
				t.Fatal("joined metadata lost the chat route or exposed primary project names", args)
			}
		case <-time.After(time.Second):
			t.Fatal("joined route metadata was not updated")
		}
		release <- struct{}{}
		<-done
	}
}

func TestCrossNetworkDirectoryOmitsProjectContext(t *testing.T) {
	raw := directoryWithoutProjectContext(json.RawMessage(`{"agents":[{"repository":"private-project","branch":"customer-work","activity":"Working","activity_at":"2026-10-21T07:00:00Z"}],"note":"directory"}`))
	var out struct {
		Agents []map[string]any `json:"agents"`
	}
	if json.Unmarshal(raw, &out) != nil || len(out.Agents) != 1 {
		t.Fatal("invalid projection")
	}
	a := out.Agents[0]
	if a["repository"] != nil || a["branch"] != nil || a["activity"] != "Working" || a["activity_at"] == nil {
		t.Fatal("cross-network project names or activity lost", a)
	}
}

// Windows Codex reported only a waiting toast: an empty real hook must still
// identify its route without contacting the server or admitting a message.
