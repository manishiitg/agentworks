package main

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
)

const cursorTestSession = "11111111-2222-4333-8444-555555555555"

func TestCursorNativeProcessAndDiscovery(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux kernel process/launcher regression")
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
		t.Fatal("native kernel identity failed", err)
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
	_, err = cursorCallingContext()
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
	text := cursorFollowup(state, cursorTestSession, "lease", false)
	if !strings.Contains(text, "First valid") || !strings.Contains(text, "Last valid") || strings.Contains(text, "DO_NOT_RENDER") || !strings.Contains(text, "explicit recovery") {
		t.Fatal("mixed offers lost valid neighbors or recovery")
	}
	if cursorFollowup(state, cursorTestSession, "old", false) != "" {
		t.Fatal("followed a replacement attachment")
	}
	state.Messages = nil
	if !strings.Contains(cursorFollowup(state, cursorTestSession, "lease", true), "--attachment-id") {
		t.Fatal("ambiguous drain lost its original attachment recovery")
	}
	// Launch-prefix validation must stop before private arguments.
	args := "/example/cursor-agent\x00--use-system-ca\x00/example/index.js\x00PRIVATE_ARGUMENT\x00"
	r := strings.NewReader(args)
	if _, _, err := cursorLeadingEntry(r); err != nil || r.Len() != len("PRIVATE_ARGUMENT\x00") {
		t.Fatal("process verifier consumed prompt arguments", err)
	}
}
