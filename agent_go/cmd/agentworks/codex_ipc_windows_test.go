package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Microsoft/go-winio"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
	"golang.org/x/sys/windows"
)

// PLAT-856-500: exercise the real named pipe and OS token/image checks. An
// unapproved server receives no bytes; the admitted same-user fixture receives
// only an exact-owner AgentWorks tool output, including the idle start path.
// This is protocol/OS proof, not a real Windows Codex app acceptance test.
func TestWindowsCodexPipeAuthenticatesBeforeSendingAndUsesExactOwner(t *testing.T) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	listener, err := winio.ListenPipe(codexWindowsPipe, &winio.PipeConfig{SecurityDescriptor: "D:P(A;;GA;;;SY)(A;;GA;;;" + user.User.Sid.String() + ")"})
	if err != nil {
		t.Fatal(err) // do not touch any other running app or pipe
	}
	defer listener.Close()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	previous := codexWindowsPackageRoot
	defer func() { codexWindowsPackageRoot = previous }()
	refused := make(chan error, 1)
	go func() {
		conn, e := listener.Accept()
		if e != nil {
			refused <- e
			return
		}
		defer conn.Close()
		_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
		one := make([]byte, 1)
		n, e := conn.Read(one)
		if n != 0 || !errors.Is(e, io.EOF) {
			refused <- fmt.Errorf("unapproved server received bytes or was not closed: n=%d err=%v", n, e)
			return
		}
		refused <- nil
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if conn, err := dialCodexDesktop(ctx, "desktop+npipe://codex-ipc"); err == nil {
		conn.Close()
		t.Fatal("the test executable is not an installed Codex app and must be refused")
	}
	if err := <-refused; err != nil {
		t.Fatal(err)
	}
	// Admit only this fixture's actual image directory, substituting the package
	// install folder. SID/PID/handle checks still read the real Windows process.
	codexWindowsPackageRoot = func(uint32) (string, error) { return filepath.Dir(exe), nil }
	if codexWindowsImageUnder(exe, []string{filepath.Dir(exe) + "-other"}) {
		t.Fatal("an adjacent installation directory must not authenticate the image")
	}
	if codexWindowsPackageAllowed("OpenAI.Codex_0000000000000") || !codexWindowsPackageAllowed("OpenAI.Codex_2p2nqsd0c76g0") {
		t.Fatal("only the Codex package from OpenAI's publisher may serve the pipe")
	}
	done := make(chan error, 1)
	go func() {
		conn, e := listener.Accept()
		if e != nil {
			done <- e
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
		var deliveryID string
		for _, method := range []string{"initialize", "thread-owner-discovery", "thread-follower-steer-turn", "thread-follower-start-turn"} {
			header := make([]byte, 4)
			if _, e = io.ReadFull(conn, header); e != nil {
				done <- e
				return
			}
			length := binary.LittleEndian.Uint32(header)
			if length == 0 || length > 1<<20 {
				done <- errors.New("invalid native frame")
				return
			}
			data := make([]byte, length)
			if _, e = io.ReadFull(conn, data); e != nil {
				done <- e
				return
			}
			var request struct {
				Method, RequestID, TargetClientID string
				Params                            map[string]json.RawMessage
			}
			if e = json.Unmarshal(data, &request); e != nil || request.Method != method {
				done <- errors.New("wrong native request")
				return
			}
			reply := map[string]any{"type": "response", "requestId": request.RequestID, "resultType": "success"}
			switch method {
			case "initialize":
				reply["result"] = map[string]any{"clientId": "fixture-client"}
			case "thread-owner-discovery":
				reply["handledByClientId"] = "exact-owner"
				reply["result"] = map[string]any{"supportsUntrustedAppInput": true}
			default:
				if request.TargetClientID != "exact-owner" {
					done <- errors.New("offer targeted another client")
					return
				}
				var conversation string
				_ = json.Unmarshal(request.Params["conversationId"], &conversation)
				if conversation != "00000000-0000-0000-0000-000000000001" {
					done <- errors.New("offer targeted another chat")
					return
				}
				var tool struct{ Namespace, Name string }
				if method == "thread-follower-steer-turn" {
					_ = json.Unmarshal(request.Params["toolOutput"], &tool)
					_ = json.Unmarshal(request.Params["clientUserMessageId"], &deliveryID)
					reply["resultType"], reply["error"] = "error", "no active turn"
				} else {
					var turn struct {
						Request struct {
							ToolOutput          json.RawMessage
							ClientUserMessageID string
						}
					}
					_ = json.Unmarshal(request.Params["turnStart"], &turn)
					_ = json.Unmarshal(turn.Request.ToolOutput, &tool)
					if deliveryID == "" || turn.Request.ClientUserMessageID != deliveryID {
						done <- errors.New("idle start changed the delivery identity")
						return
					}
				}
				if tool.Namespace != "agentworks" || tool.Name != "agent_message" {
					done <- errors.New("peer content lost tool-output provenance")
					return
				}
			}
			body, _ := json.Marshal(reply)
			binary.LittleEndian.PutUint32(header, uint32(len(body)))
			if _, e = conn.Write(append(header, body...)); e != nil {
				done <- e
				return
			}
		}
		done <- nil
	}()
	err = codexDesktopOffer(ctx, "desktop+npipe://codex-ipc", agentworksclient.LocalBridgeReply{Attachment: agentworksclient.AgentAttachment{Provider: "codex", SessionID: "00000000-0000-0000-0000-000000000001"}, Messages: []agentworksclient.AgentLiveMessage{{MessageID: "fixture-message", Message: "peer content"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
