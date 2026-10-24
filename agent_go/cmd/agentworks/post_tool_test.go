package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
)

func TestPostToolHookBudgetAndContext(t *testing.T) {
	root, err := os.MkdirTemp("", "aw-post-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	socket := filepath.Join(root, "bridge.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Skip(err)
	}
	if err = os.Chmod(socket, 0600); err != nil {
		t.Fatal(err)
	}
	var mode atomic.Int32
	var receipts atomic.Int32
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in agentworksclient.LocalBridgeRequest
		if json.NewDecoder(r.Body).Decode(&in) != nil {
			t.Error("invalid post-tool request")
			return
		}
		if in.Action == "ack" {
			if in.Delivery != "delivered" || in.MessageID != "message" || in.SessionID != cursorTestSession || in.HostKey != "fixture-host" || in.Transport != in.Provider+"_hook" {
				t.Error("wrong delivered receipt", in.Action, in.Delivery, in.Transport)
			}
			receipts.Add(1)
			if mode.Load() == 3 {
				_ = json.NewEncoder(w).Encode(agentworksclient.LocalBridgeReply{Error: "fixture receipt refused"})
				return
			}
			if mode.Load() == 4 {
				<-r.Context().Done()
				return
			}
			_ = json.NewEncoder(w).Encode(agentworksclient.LocalBridgeReply{Attached: true})
			return
		}
		if in.Action != "post_tool" {
			t.Error("unexpected hook action", in.Action)
			return
		}
		if mode.Load() == 2 {
			<-r.Context().Done()
			return
		}
		out := agentworksclient.LocalBridgeReply{}
		if mode.Load() == 1 || mode.Load() >= 3 {
			out.Attached = true
			out.Attachment = agentworksclient.AgentAttachment{ID: "lease", Provider: in.Provider, SessionID: in.SessionID}
			out.Messages = []agentworksclient.AgentLiveMessage{{AttachmentID: "lease", MessageID: "message", InboxID: "inbox", Sender: "reviewer", Message: "MID_TURN_MARKER", Sequence: 1}}
		}
		_ = json.NewEncoder(w).Encode(out)
	})}
	go srv.Serve(listener)
	defer srv.Close()
	for _, provider := range []string{"claude", "codex", "cursor"} {
		for i, name := range []string{"empty", "pending", "slow", "refused_receipt", "slow_receipt"} {
			mode.Store(int32(i))
			var output bytes.Buffer
			before, _ := os.ReadFile(filepath.Join(root, "bridge.log"))
			beforeReceipts := receipts.Load()
			start := time.Now()
			err := postToolMessages(context.Background(), root, &options{stdout: &output}, agentworksclient.LocalBridgeRequest{Provider: provider, SessionID: cursorTestSession, HostKey: "fixture-host"})
			elapsed := time.Since(start)
			t.Logf("provider=%s case=%s elapsed=%s", provider, name, elapsed)
			if err != nil || elapsed > 2*time.Second {
				t.Fatalf("post-tool exceeded budget: %v %s", err, elapsed)
			}
			after, _ := os.ReadFile(filepath.Join(root, "bridge.log"))
			if i == 0 || i == 2 {
				if receipts.Load() != beforeReceipts {
					t.Fatal("empty/error hook acknowledged an unseen message")
				}
				if !bytes.Equal(before, after) {
					t.Fatal("empty/error hook logged a render")
				}
				if output.Len() != 0 {
					t.Fatal("empty/error post-tool emitted output")
				}
				continue
			}
			if receipts.Load() != beforeReceipts+1 {
				t.Fatal("rendered post-tool message has no delivered receipt")
			}
			trace := string(after[len(before):])
			if !strings.Contains(trace, "event=PostToolUse transport="+provider+"_hook outcome=rendered message=message") || !strings.Contains(trace, "rendered_at=") || strings.Contains(trace, "MID_TURN_MARKER") {
				t.Fatal("hook render trace missing or contains body", trace)
			}
			outcome := "recorded"
			if i >= 3 {
				outcome = "failed"
			}
			if !strings.Contains(trace, "event=receipt transport="+provider+"_hook outcome="+outcome+" message=message inbox=inbox attachment=lease") || !strings.Contains(trace, "elapsed_ms=") || !strings.Contains(trace, "budget_ms=") || !strings.Contains(trace, "observed_at=") {
				t.Fatal("receipt outcome missing", trace)
			}
			if i == 3 && !strings.Contains(trace, `cause=helper_refused detail="fixture receipt refused"`) || i == 4 && !strings.Contains(trace, `cause=context_done detail="context deadline exceeded"`) {
				t.Fatal("receipt failure cause missing", trace)
			}
			if !strings.Contains(output.String(), "MID_TURN_MARKER") || !strings.Contains(strings.ToLower(output.String()), "acknowledge delivered once") {
				t.Fatal("body or receipt reminder lost", output.String())
			}
			if provider == "codex" && (!strings.Contains(output.String(), `"hookEventName":"PostToolUse"`) || !strings.Contains(output.String(), `"additionalContext"`)) {
				t.Fatal(output.String())
			}
			if provider == "cursor" && !strings.Contains(output.String(), `"additional_context"`) {
				t.Fatal(output.String())
			}
		}
	}
}
