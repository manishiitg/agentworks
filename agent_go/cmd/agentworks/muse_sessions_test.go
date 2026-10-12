package main

import (
	"bufio"
	"bytes"
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
	"github.com/mark3labs/mcp-go/mcp"
)

// Exercise real ps/process-start fencing under the versioned executable that
// caused 866-6. No real account, provider trust, attachment or listener is used.
func TestMusePluginExactProcessBoundary(t *testing.T) {
	const session = "11111111-2222-4333-8444-555555555555"
	if runtime.GOOS == "windows" {
		t.Skip("Muse native process contract is Unix-only")
	}
	switch os.Getenv("AGENTWORKS_MUSE_FIXTURE") {
	case "duplicate":
		host, err := providerPIDHostKey("muse", os.Getpid())
		if err != nil {
			t.Fatal(err)
		}
		root, _ := agentworksclient.DefaultLocalBridgeRoot()
		observeMuseSession(root, os.Getpid(), host, pluginHookInput{SessionID: session, Event: "Stop"})
		fmt.Fprintln(os.Stdout, "ready")
		_, _ = io.Copy(io.Discard, os.Stdin)
		return
	case "client":
		host, err := providerHostKey("muse")
		if err != nil || host != os.Getenv("AGENTWORKS_MUSE_HOST") {
			t.Fatal("versioned ancestor not bound", host, err)
		}
		var stdout, stderr bytes.Buffer
		for _, prompt := range []string{"/agentworks-muse:agentworks", "/agentworks-muse:agentworks attach", "/agentworks-muse:agentworks detach " + agentDeliveryHeader} {
			stdout.Reset()
			input, _ := json.Marshal(pluginHookInput{SessionID: session, Event: "UserPromptSubmit", Prompt: prompt, Queued: true, Cwd: "/example/project"})
			if code := run(context.Background(), []string{"plugin", "hook", "muse"}, bytes.NewReader(input), &stdout, &stderr, os.Getenv); code != 0 || !strings.Contains(stdout.String(), "Agent messages cannot") {
				t.Fatal("queued Muse command reached model or attached", code, stdout.String(), stderr.String())
			}
		}
		root, _ := agentworksclient.DefaultLocalBridgeRoot()
		if _, err = os.Stat(root); !os.IsNotExist(err) {
			t.Fatal("unattached hook started bridge", err)
		}
		server, err := agentworksclient.NewPluginMCPServer(context.Background(), root, "muse", host, false)
		if err != nil || len(server.ListTools()) != 6 || server.ListTools()["agent_message_ack"] == nil || server.ListTools()["agent_message_set_purpose"] == nil {
			t.Fatal("Muse messaging child unavailable or overbroad", err)
		}
		if _, err = agentworksclient.NewPluginMCPServer(context.Background(), root, "muse", host, true); err == nil {
			t.Fatal("Muse obtained Claude native-channel authority")
		}
		return
	case "host":
		host, err := providerPIDHostKey("muse", os.Getpid())
		if err != nil || !providerHostAlive("muse", host) {
			t.Fatal("versioned process/truncated comm not recognized", err)
		}
		child := exec.Command(os.Getenv("AGENTWORKS_MUSE_TEST_EXE"), "-test.run=^TestMusePluginExactProcessBoundary$")
		child.Env = append(os.Environ(), "AGENTWORKS_MUSE_FIXTURE=client", "AGENTWORKS_MUSE_HOST="+host)
		if output, err := child.CombinedOutput(); err != nil {
			t.Fatal(err, string(output))
		}
		root, _ := agentworksclient.DefaultLocalBridgeRoot()
		found := discoverMuseSessions(root)
		if len(found) != 1 || found[0].session != session || found[0].host != host {
			t.Fatal("terminal discovery lost exact observed UUID/process", found)
		}
		path := filepath.Join(museSessionDirectory(root), museObservationName(host))
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("discovery metadata is not private", err)
		}
		input := pluginHookInput{SessionID: session, Event: "Stop"}
		if err = os.Remove(path); err != nil {
			t.Fatal(err)
		}
		observeMuseSession(root, os.Getpid(), "wrong-start-time", input)
		if len(discoverMuseSessions(root)) != 0 {
			t.Fatal("stale/PID-reused process accepted")
		}
		observeMuseSession(root, os.Getpid(), host, input)
		// A UUID observed on two still-live processes must not be guessed.
		exe, _ := os.Executable()
		duplicate := exec.Command(exe, "-test.run=^TestMusePluginExactProcessBoundary$")
		duplicate.Env = append(os.Environ(), "AGENTWORKS_MUSE_FIXTURE=duplicate")
		stdin, _ := duplicate.StdinPipe()
		stdout, _ := duplicate.StdoutPipe()
		if err = duplicate.Start(); err != nil {
			t.Fatal(err)
		}
		defer duplicate.Process.Kill()
		if line, err := bufio.NewReader(stdout).ReadString('\n'); err != nil || line != "ready\n" || len(discoverMuseSessions(root)) != 0 {
			t.Fatal("same UUID on two processes accepted", line, err)
		}
		stdin.Close()
		if err = duplicate.Wait(); err != nil {
			t.Fatal(err)
		}
		if len(discoverMuseSessions(root)) != 1 {
			t.Fatal("dead duplicate was not pruned")
		}
		// Reads must reject symlinks even inside a private directory.
		if err = os.Rename(path, path+".saved"); err != nil {
			t.Fatal(err)
		}
		if err = os.Symlink(path+".saved", path); err != nil {
			t.Fatal(err)
		}
		if len(discoverMuseSessions(root)) != 0 {
			t.Fatal("symlink discovery record followed")
		}
		os.Remove(path)
		os.Rename(path+".saved", path)

		setup := museAttachSetup(agentworksclient.LocalBridgeReply{Attachment: agentworksclient.AgentAttachment{Provider: "muse", SessionID: session, AgentName: "fixture-muse", Purpose: "PEER_TEXT_MUST_NOT_BE_TRUSTED"}, LastError: "PEER_TEXT_MUST_NOT_BE_TRUSTED"})
		if strings.Contains(setup, "PEER_TEXT_MUST_NOT_BE_TRUSTED") || !strings.Contains(setup, "agent_message_monitor") {
			t.Fatal("Muse trusted bootstrap contains model-written text", setup)
		}
		// A bash spool cannot masquerade as the native Monitor for this UUID.
		data, err := filepath.EvalSymlinks(t.TempDir()) // macOS /var aliases /private/var
		if err != nil {
			t.Fatal(err)
		}
		t.Setenv("XDG_DATA_HOME", data)
		t.Setenv("MUSE_TOOL_USE_ID", "fixture-call")
		spool := filepath.Join(data, "muse", "sessions", "2026", "10", "12", session, "tool-outputs", ".spool")
		if err = os.MkdirAll(spool, 0700); err != nil {
			t.Fatal(err)
		}
		bash := filepath.Join(spool, "fixture-call-bash.txt.tmp")
		if err = os.WriteFile(bash, nil, 0600); err != nil {
			t.Fatal(err)
		}
		if museMonitorArtifact(session) {
			t.Fatal("bash listener accepted as Monitor")
		}
		monitor := filepath.Join(spool, "fixture-call-monitor-stdout.txt.tmp")
		if err = os.WriteFile(monitor, nil, 0600); err != nil {
			t.Fatal(err)
		}
		if !museMonitorArtifact(session) || museMonitorArtifact("11111111-2222-4333-8444-555555555556") {
			t.Fatal("Monitor spool lost exact session")
		}
		os.Remove(monitor)
		if err = os.Symlink(bash, monitor); err != nil {
			t.Fatal(err)
		}
		if museMonitorArtifact(session) {
			t.Fatal("Monitor followed a linked spool")
		}
		os.Remove(monitor)
		var seen []string
		server, err := agentworksclient.NewMusePluginMCPServer(context.Background(), root, host, func() (string, error) {
			id, err := museSessionForHost(root, host)
			seen = append(seen, id)
			return id, err
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(server.ListTools()) != 8 || server.ListTools()["agent_message_monitor"] == nil || server.ListTools()["agent_message_receive"] == nil {
			t.Fatal("Muse delivery helper tools missing")
		}
		_, _ = server.ListTools()["agent_message_peers"].Handler(context.Background(), mcp.CallToolRequest{})
		input.SessionID = "11111111-2222-4333-8444-555555555556"
		input.Event = "UserPromptSubmit"
		observeMuseSession(root, os.Getpid(), host, input)
		_, _ = server.ListTools()["agent_message_peers"].Handler(context.Background(), mcp.CallToolRequest{})
		if len(seen) != 2 || seen[0] != session || seen[1] != input.SessionID {
			t.Fatal("MCP retained the previous chat identity", seen)
		}
		current := input.SessionID
		input.SessionID = session
		input.Event = "Stop"
		observeMuseSession(root, os.Getpid(), host, input)
		input.Event = "SessionEnd"
		observeMuseSession(root, os.Getpid(), host, input)
		if id, _ := museSessionForHost(root, host); id != current {
			t.Fatal("late old Stop/SessionEnd changed the current chat", id)
		}
		input.SessionID = current
		observeMuseSession(root, os.Getpid(), host, input)
		if len(discoverMuseSessions(root)) != 0 {
			t.Fatal("ended session still selectable")
		}
		return
	}
	for _, tc := range []struct {
		goos, name string
		accepted   bool
	}{
		{"linux", "muse-bin-1.4.4-R5419.1", true},
		{"darwin", "muse-bin-1.4.4-R5419.1", true},
		{"linux", "muse-bin-1.4.4-", true},
		{"darwin", "muse-bin-1.4.4-R", true},
		{"darwin", "muse-bin-1.4.4-", false},
		{"linux", "muse-bin-1.4.4-r5419.1", false},
	} {
		if providerProcessMatchesOnOS("muse", tc.name, tc.goos) != tc.accepted {
			t.Fatal("versioned/truncated executable mismatch", tc)
		}
	}
	for _, command := range []string{"muse-bin-evil", "muse-bin-1.4.4-helper", "not-muse-bin-1.4.4", "codex"} {
		if providerProcessMatches("muse", command) {
			t.Fatal("unrelated executable admitted", command)
		}
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(t.TempDir(), "muse-bin-1.4.4-R5419.1")
	if err = os.WriteFile(fixture, binary, 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, fixture, "-test.run=^TestMusePluginExactProcessBoundary$")
	child.Env = append(os.Environ(), "XDG_CACHE_HOME="+t.TempDir(), "AGENTWORKS_MUSE_FIXTURE=host", "AGENTWORKS_MUSE_TEST_EXE="+exe)
	if output, err := child.CombinedOutput(); err != nil {
		t.Fatal("Muse process regression", err, string(output))
	}
}
