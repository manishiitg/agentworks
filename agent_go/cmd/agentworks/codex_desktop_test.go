package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
)

// An ambiguous native write must never fall through to starting another turn.
// Also pin exact-owner routing and truthful tool-output provenance.
func TestCodexDesktopAmbiguousOfferDoesNotStartTurn(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(root, "desktop.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan error, 1)
	go func() {
		conn, e := listener.Accept()
		if e != nil {
			done <- e
			return
		}
		defer conn.Close()
		methods := []string{"initialize", "thread-owner-discovery", "thread-follower-steer-turn"}
		for _, method := range methods {
			h := make([]byte, 4)
			if _, e = io.ReadFull(conn, h); e != nil {
				done <- e
				return
			}
			b := make([]byte, binary.LittleEndian.Uint32(h))
			if _, e = io.ReadFull(conn, b); e != nil {
				done <- e
				return
			}
			var v struct {
				Method string                     `json:"method"`
				ID     string                     `json:"requestId"`
				Target string                     `json:"targetClientId"`
				Params map[string]json.RawMessage `json:"params"`
			}
			if e = json.Unmarshal(b, &v); e != nil {
				done <- e
				return
			}
			if v.Method != method {
				done <- io.ErrUnexpectedEOF
				return
			}
			var result any = map[string]any{"clientId": "agentworks-peer"}
			if method == "thread-owner-discovery" {
				result = map[string]any{"supportsUntrustedAppInput": true}
			}
			if method == "thread-follower-steer-turn" {
				var tool map[string]any
				_ = json.Unmarshal(v.Params["toolOutput"], &tool)
				var session string
				_ = json.Unmarshal(v.Params["conversationId"], &session)
				if v.Target != "exact-owner" || session != "00000000-0000-0000-0000-000000000001" || tool["namespace"] != "agentworks" || tool["name"] != "agent_message" {
					done <- io.ErrUnexpectedEOF
					return
				}
				// Drop after receiving the offer. It could have been accepted.
				done <- nil
				return
			}
			reply, _ := json.Marshal(map[string]any{"type": "response", "requestId": v.ID, "method": method, "resultType": "success", "handledByClientId": "exact-owner", "result": result})
			binary.LittleEndian.PutUint32(h, uint32(len(reply)))
			if _, e = conn.Write(append(h, reply...)); e != nil {
				done <- e
				return
			}
		}
	}()
	err = codexDesktopOffer(context.Background(), "desktop+unix://"+socket, agentworksclient.LocalBridgeReply{Attachment: agentworksclient.AgentAttachment{SessionID: "00000000-0000-0000-0000-000000000001", Provider: "codex"}, Messages: []agentworksclient.AgentLiveMessage{{MessageID: "message", Message: "peer content"}}})
	if err == nil {
		t.Fatal("disconnect fabricated admission")
	}
	if e := <-done; e != nil {
		t.Fatal(e)
	}
}
