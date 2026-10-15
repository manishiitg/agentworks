package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
)

const cursorTestSession = "11111111-2222-4333-8444-555555555555"

func TestCursorNativeProcessAndDiscovery(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("kernel process/launcher regression runs on Linux and macOS")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal("native process regression requires Node", err)
	}
	root := t.TempDir()
	dir := filepath.Join(root, "cursor-agent", "versions", "fixture")
	if err = os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	reader, err := os.Open(node)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	image, err := os.OpenFile(filepath.Join(dir, "node"), os.O_CREATE|os.O_WRONLY, 0755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = io.Copy(image, reader); err != nil {
		t.Fatal(err)
	}
	if err = image.Close(); err != nil {
		t.Fatal(err)
	}
	launcher := filepath.Join(dir, "cursor-agent")
	if err = os.WriteFile(launcher, []byte("#!/bin/sh\n"), 0700); err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(dir, "index.js")
	script := `const cp=require('node:child_process');
function child(mode){const r=cp.spawnSync(process.env.CURSOR_TEST_EXE,['-test.run','^TestCursorNativeChild$'],{env:{...process.env,CURSOR_TEST_CHILD:mode},encoding:'utf8'});if(r.status!==0){process.stderr.write(r.stdout+r.stderr);process.exit(1);}}
if(process.argv[2]==='nested'){child('ambiguous');}else{child('observe');const r=cp.spawnSync(process.execPath,[__filename,'nested'],{argv0:process.env.CURSOR_TEST_LAUNCHER,env:process.env,encoding:'utf8'});if(r.status!==0){process.stderr.write(r.stdout+r.stderr);process.exit(1);}child('close');}`
	if err = os.WriteFile(entry, []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(filepath.Join(dir, "node"), entry)
	command.Args[0] = launcher
	command.Env = append(os.Environ(), "CURSOR_TEST_EXE="+self, "CURSOR_TEST_LAUNCHER="+launcher,
		"CURSOR_CONVERSATION_ID="+cursorTestSession, "XDG_CACHE_HOME="+filepath.Join(root, "cache"))
	if out, e := command.CombinedOutput(); e != nil {
		t.Fatalf("real Node child/context regression: %v\n%s", e, out)
	}
}

// The fixture invokes this real test binary beneath a verified Node launcher.
// No daemon, account registration, authentication or person command is faked.
func TestCursorNativeChild(t *testing.T) {
	mode := os.Getenv("CURSOR_TEST_CHILD")
	if mode == "" {
		t.Skip("native fixture child only")
	}
	process, err := cursorAncestor()
	if err != nil || process.Kind != "cli" || !cursorProcessForHost(process.Host) {
		_, detail := cursorProcessAt(os.Getppid())
		t.Fatal("native kernel identity failed", err, "| parent check:", detail)
	}
	root, err := agentworksclient.DefaultLocalBridgeRoot()
	if err != nil {
		t.Fatal(err)
	}
	input := pluginHookInput{SessionID: cursorTestSession, Event: "SessionStart", Cwd: "/example/project"}
	if mode == "close" {
		own, e := cursorCallingContext()
		if e != nil || own.host != process.Host || own.session != cursorTestSession {
			t.Fatal("stale nested process was selected", e)
		}
		if cursorPinSession("aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee", own) == nil {
			t.Fatal("cross-conversation pin accepted")
		}
		input.Event = "SessionEnd"
		if e = observeCursor(root, process, input); e != nil {
			t.Fatal(e)
		}
		if _, e = cursorCallingContext(); e == nil {
			t.Fatal("closed native observation still selected")
		}
		return
	}
	if err = observeCursor(root, process, input); err != nil {
		t.Fatal(err)
	}
	if mode == "observe" {
		dir, e := openMuseObservationDirectory(cursorObservationDirectory(root), false)
		if e != nil {
			t.Fatal(e)
		}
		dead := cursorProcess{PID: 99999999, Host: cursorHostKey(99999999, strings.Repeat("0", 64)), Kind: "cli"}
		for i := 0; i < 300; i++ {
			record := cursorObservation{Session: fmt.Sprintf("%08x-2222-4333-8444-555555555555", i+1), PID: dead.PID, Host: dead.Host, Kind: dead.Kind}
			raw, _ := json.Marshal(record)
			if e = writeMuseObservation(dir, cursorObservationName(record.Host, record.Session), raw); e != nil {
				t.Fatal(e)
			}
		}
		dir.Close()
	}
	_, err = cursorCallingContext()
	if mode == "observe" && err == nil {
		own, fromAgent, e := ownAgentSession(context.Background(), root, &options{getenv: os.Getenv})
		if e != nil || !fromAgent || own.provider != "cursor" || own.host != process.Host || own.session != cursorTestSession {
			t.Fatal("own CLI context did not use the exact Cursor verifier", e)
		}

		files, e := os.ReadDir(cursorObservationDirectory(root))
		if e != nil || len(files) != 1 {
			t.Fatal("dead observations accumulated", e)
		}
		if cursorRequireHelperLease(liveAgentSession{host: process.Host}, "") == nil {
			t.Fatal("helper accepted no original attachment")
		}
		// Reproduce a second observed UUID on one app host: even if a forged env
		// derives it, the desktop helper policy must refuse the shared-host route.
		other := input
		other.SessionID = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
		if e = observeCursor(root, process, other); e != nil {
			t.Fatal(e)
		}
		t.Setenv("CURSOR_CONVERSATION_ID", other.SessionID)
		spoofed, e := cursorCallingContext()
		if e != nil || spoofed.session != other.SessionID {
			t.Fatal("shared-host spoof fixture failed", e)
		}
		if cursorHelperAllowed("desktop", "other-lease") == nil {
			t.Fatal("desktop shared host accepted a spoofed UUID")
		}
		other.Event = "SessionEnd"
		if e = observeCursor(root, process, other); e != nil {
			t.Fatal(e)
		}
	}
	if mode == "ambiguous" && err == nil {
		t.Fatal("two live native UUIDs selected a last-used host")
	}
	if mode == "observe" && err != nil {
		t.Fatal(err)
	}
}

func TestCursorHookLeaseAndRecovery(t *testing.T) {
	input := pluginHookInput{CursorConversation: cursorTestSession, SessionID: "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee", Event: "stop", CursorLoop: json.RawMessage("0"), CursorStatus: "completed"}
	if normalizeCursorHook(&input) == nil {
		t.Fatal("conflicting native UUIDs accepted")
	}
	input.SessionID = cursorTestSession
	if err := normalizeCursorHook(&input); err != nil || input.Event != "Stop" {
		t.Fatal("native hook normalization failed", err)
	}
	state := agentworksclient.LocalBridgeReply{Attached: true, Attachment: agentworksclient.AgentAttachment{ID: "lease", Provider: "cursor", SessionID: cursorTestSession}}
	state.Messages = []agentworksclient.AgentLiveMessage{
		{AttachmentID: "lease", MessageID: "first", InboxID: "inbox", Sender: "worker", Message: "First valid", Sequence: 1},
		{AttachmentID: "old", MessageID: "stale", InboxID: "inbox", Sender: "worker", Message: "DO_NOT_RENDER", Sequence: 2},
		{AttachmentID: "lease", MessageID: "last", InboxID: "inbox", Sender: "worker", Message: "Last valid", Sequence: 3},
	}
	text := cursorFollowup(state, cursorTestSession, "lease", false, false)
	if !strings.Contains(text, "First valid") || !strings.Contains(text, "Last valid") || strings.Contains(text, "DO_NOT_RENDER") || !strings.Contains(text, "explicit recovery") {
		t.Fatal("mixed offers lost valid neighbors or recovery")
	}
	if cursorFollowup(state, cursorTestSession, "old", false, false) != "" {
		t.Fatal("followed a replacement attachment")
	}
	state.Messages = nil
	if !strings.Contains(cursorFollowup(state, cursorTestSession, "lease", true, false), "--attachment-id") {
		t.Fatal("ambiguous drain lost its original attachment recovery")
	}
	// Launch-prefix validation must stop before private arguments.
	args := "/example/cursor-agent\x00--use-system-ca\x00/example/index.js\x00PRIVATE_ARGUMENT\x00"
	r := strings.NewReader(args)
	if _, _, err := cursorLeadingEntry(r); err != nil || r.Len() != len("PRIVATE_ARGUMENT\x00") {
		t.Fatal("process verifier consumed prompt arguments", err)
	}
}

// The desktop app shares one MCP server between chats: a call is served only for
// the one chat whose hook announced exactly that call, once, before it expires.
func TestCursorDesktopFenceIsOneShotAndPerChat(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Cursor desktop messaging needs owner-only record files")
	}
	root := filepath.Join(t.TempDir(), "bridge")
	now := time.Now()
	const chatA, chatB = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee", "bbbbbbbb-cccc-4ddd-8eee-ffffffffffff"
	host := cursorHostKey(4242, strings.Repeat("a", 64))
	ack := map[string]any{"message_id": "m1", "delivery": "admitted"}
	if err := writeCursorFence(root, host, chatA, "agentworks-messages-agent_message_ack", `{"delivery":"admitted","message_id":"m1"}`, now); err != nil {
		t.Fatal(err)
	}
	if chat, err := consumeCursorFence(root, host, "agent_message_ack", ack, now); err != nil || chat != chatA {
		t.Fatal("announced call was not matched to its chat", chat, err)
	}
	if _, err := consumeCursorFence(root, host, "agent_message_ack", ack, now); err == nil {
		t.Fatal("a fence was used twice")
	}
	// Two identical calls in a row from one chat: each announced call is served once.
	for i := 0; i < 2; i++ {
		if err := writeCursorFence(root, host, chatA, "agent_message_read", "{}", now); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		if chat, err := consumeCursorFence(root, host, "agent_message_read", map[string]any{}, now); err != nil || chat != chatA {
			t.Fatal("a repeated call from one chat lost its record", i, err)
		}
	}
	// Identical calls from two chats cannot be told apart: refuse, never pick one.
	for _, chat := range []string{chatA, chatB} {
		if err := writeCursorFence(root, host, chat, "agent_message_peers", "", now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := consumeCursorFence(root, host, "agent_message_peers", nil, now); err == nil {
		t.Fatal("an ambiguous call was served")
	}
	if err := writeCursorFence(root, host, chatB, "agent_message_read", `{"inbox_id":"i"}`, now.Add(-3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := consumeCursorFence(root, host, "agent_message_read", map[string]any{"inbox_id": "i"}, now); err == nil {
		t.Fatal("an expired fence was served")
	}
	if err := writeCursorFence(root, host, chatB, "agent_message_read", `{"inbox_id":"i"}`, now); err != nil {
		t.Fatal(err)
	}
	if _, err := consumeCursorFence(root, cursorHostKey(4243, strings.Repeat("b", 64)), "agent_message_read", map[string]any{"inbox_id": "i"}, now); err == nil {
		t.Fatal("another Cursor app consumed this app's fence")
	}
}
