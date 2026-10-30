package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
	"github.com/manishiitg/coding-agent-loop/workspace/localfiles"
)

func TestExecutorRetriesAStaleDeviceConnection(t *testing.T) {
	var attempts atomic.Int32
	connected := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("inspect") == "1" {
			fmt.Fprint(w, `{"owner_id":"test-owner"}`)
			return
		}
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		var hello localfiles.Hello
		if conn.ReadJSON(&hello) != nil {
			return
		}
		if attempts.Add(1) == 1 {
			_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseTryAgainLater, "old socket still closing"), time.Now().Add(time.Second))
			return
		}
		_ = conn.WriteJSON(map[string]bool{"connected": true})
		close(connected)
		var request any
		_ = conn.ReadJSON(&request)
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var stdout, stderr bytes.Buffer
	finished := make(chan int, 1)
	args := []string{"--server", server.URL, "--config", filepath.Join(t.TempDir(), "executor.json"), "start", "--foreground", "--device", "laptop"}
	go func() {
		finished <- run(ctx, args, strings.NewReader(""), &stdout, &stderr, func(key string) string {
			if key == "AGENTWORKS_TOKEN" {
				return "test-token"
			}
			return ""
		})
	}()
	select {
	case <-connected:
	case code := <-finished:
		t.Fatalf("quit during transient collision: %d", code)
	case <-time.After(5 * time.Second):
		t.Fatal("did not reconnect")
	}
	cancel()
	select {
	case code := <-finished:
		if code != 0 || attempts.Load() != 2 {
			t.Fatalf("reconnect failed: %d (%d attempts) %s", code, attempts.Load(), stderr.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("did not stop")
	}
}

func init() { openWebsite = func(string) {} } // tests never open a browser

// `agentworks start` shares the folder you are standing in, read and write with shell, named after the folder and the
// computer, with no flags: the one-command flow the setup page describes.
func TestStartConnectsComputerWithoutSharingCurrentFolder(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("APPDATA", home) // the config folder on Windows
	t.Setenv("USERPROFILE", home)
	project := filepath.Join(t.TempDir(), "My App")
	if err := os.Mkdir(project, 0700); err != nil {
		t.Fatal(err)
	}
	t.Chdir(project)
	received := make(chan localfiles.Hello, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("inspect") == "1" {
			fmt.Fprint(w, `{"owner_id":"test-owner"}`)
			return
		}
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		var hello localfiles.Hello
		if conn.ReadJSON(&hello) != nil || conn.WriteJSON(map[string]bool{"connected": true}) != nil {
			return
		}
		received <- hello
		var request any
		_ = conn.ReadJSON(&request)
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var stdout, stderr bytes.Buffer
	finished := make(chan int, 1)
	go func() {
		finished <- run(ctx, []string{"--server", server.URL, "start", "--foreground"}, strings.NewReader(""), &stdout, &stderr, func(key string) string {
			if key == "AGENTWORKS_TOKEN" {
				return "test-token"
			}
			return ""
		})
	}()
	select {
	case hello := <-received:
		if hello.CLIVersion != cliVersion {
			t.Fatalf("the CLI must tell the server which build it is (%q), got %q", cliVersion, hello.CLIVersion)
		}
		if !strings.HasPrefix(hello.DeviceID, defaultDeviceName()+"-") || !hello.SettingsManaged || len(hello.Resources) != 0 {
			t.Fatalf("start granted a folder without Settings: %+v", hello)
		}
	case code := <-finished:
		t.Fatalf("start exited %d: %s", code, stderr.String())
	case <-time.After(10 * time.Second):
		t.Fatal("start did not connect")
	}
	cancel()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("start did not stop")
	}
}

// The answer to start's first-run question is remembered, so a later run does not ask again.
func TestStartRemembersItsFirstRunAnswers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "start-preferences.json")
	if got := loadStartPrefs(path); got.Background != nil {
		t.Fatalf("no answers yet, got %+v", got)
	}
	background := true
	saveStartPrefs(path, startPrefs{Background: &background})
	got := loadStartPrefs(path)
	if got.Background == nil || !*got.Background {
		t.Fatalf("saved answers not restored: %+v", got)
	}
}

// Without a workspace the CLI refuses to start: which Code workspace uses a folder is never guessed.

func TestDebugReportRemovesSecrets(t *testing.T) {
	home, _ := os.UserHomeDir()
	report := redactDebug(strings.Join([]string{
		"Authorization: Bearer abcdef0123456789abcdef0123456789",
		"token=aw_pat_0123456789abcdef0123456789abcdef",
		"https://deploy:hunter2secret@example.test/repo.git",
		"code cli_verify_4a6e96b1868d4506fa43daf2d3609ce5",
		`{"refresh_token": "r-1234567890abcdef"}`,
		"export OPENAI_API_KEY=sk-abcdefghijklmnopqrstuvwx",
		"folder " + home + "/project",
		"08:07:55  shell  app  $ ls  -> 200 in 46ms  exit 0",
		"Muse watch for lease attach-3d4f2e03-9f4c-4018-9721-dd8d9c536e1a ended; session 01a135d1-019a-7582-8924-1ba2d95edd0e",
	}, "\n"))
	for _, secret := range []string{"abcdef0123456789abcdef0123456789", "aw_pat_0123", "hunter2secret", "4a6e96b1868d4506", "r-1234567890abcdef", "sk-abcdefghijklmnop"} {
		if strings.Contains(report, secret) {
			t.Fatalf("secret %q survived:\n%s", secret, report)
		}
	}
	// PLAT-856-415: redact the complete startup line before its 200-character clipping.
	line := shortStartupDetail("token=" + strings.Repeat("z", 300) + " " + strings.Repeat("界", 250))
	if strings.Contains(line, "zzzz") || len([]rune(line)) > 200 || !strings.Contains(line, "[redacted]") {
		t.Fatalf("startup detail leaked or exceeded its bound: %q", line)
	}
	if strings.Contains(report, "3d4f2e03-9f4c") || strings.Contains(report, "019a-7582") || !strings.Contains(report, "01a135d1…") {
		t.Fatalf("lease and session ids must be shortened:\n%s", report)
	}
	if !strings.Contains(report, "shell  app  $ ls  -> 200") || !strings.Contains(report, "~/project") {
		t.Fatalf("the useful activity lines must stay, and the home folder is shortened:\n%s", report)
	}
}

// Logs must not fill a disk: a file stops at its limit, only a few older files are kept, and a new run starts a new file.
func TestShareLogIsBoundedAndRotates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "k.log")
	rot, err := openRotatingLog(path, 1000, 2)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.Repeat("x", 99) + "\n"
	for i := 0; i < 100; i++ { // 10 KB written in total
		if _, err := rot.Write([]byte(line)); err != nil {
			t.Fatal(err)
		}
	}
	rot.Close()
	files, _ := filepath.Glob(path + "*")
	if len(files) != 3 { // k.log, k.log.1, k.log.2
		t.Fatalf("expected the current file and 2 older ones, got %v", files)
	}
	var total int64
	for _, f := range files {
		info, _ := os.Stat(f)
		if info.Size() > 1100 {
			t.Fatalf("%s grew past its limit: %d", f, info.Size())
		}
		total += info.Size()
	}
	if total > 3300 {
		t.Fatalf("total %d is not bounded", total)
	}
	again, err := openRotatingLog(path, 1000, 2) // a new run
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if info, _ := os.Stat(path); info.Size() != 0 {
		t.Fatal("a new run must start an empty log")
	}
}

// `agentworks start` tells the person when this server offers a newer CLI, judged by the CLI's own build (not by the whole-repository
// revision, which changes with every deploy), and stays quiet when it is current.
func TestStartSaysWhenTheServerHasANewerCLI(t *testing.T) {
	oldVersion, oldBuild := cliVersion, cliBuild
	defer func() { cliVersion, cliBuild = oldVersion, oldBuild }()
	for _, tc := range []struct {
		name                               string
		mine, myBuild, latest, latestBuild string
		hint                               bool
	}{
		{"older build", "aaaaaaa1", "build-old", "bbbbbbb2", "build-new", true},
		{"same build, newer deploy", "aaaaaaa1", "build-same", "bbbbbbb2", "build-same", false},
		{"no build ids: by revision, older", "aaaaaaa1", "", "bbbbbbb2", "", true},
		{"no build ids: by revision, current", "bbbbbbb2", "", "bbbbbbb2", "", false},
		{"a CLI too old to have a build id, server has one", "aaaaaaa1", "", "bbbbbbb2", "build-new", true},
		{"development build", "dev", "dev", "bbbbbbb2", "build-new", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cliVersion, cliBuild = tc.mine, tc.myBuild
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("APPDATA", home)
			t.Setenv("USERPROFILE", home)
			project := filepath.Join(t.TempDir(), "app")
			if err := os.Mkdir(project, 0700); err != nil {
				t.Fatal(err)
			}
			t.Chdir(project)
			connected := make(chan struct{}, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("inspect") == "1" {
					fmt.Fprint(w, `{"owner_id":"test-owner"}`)
					return
				}
				if strings.HasSuffix(r.URL.Path, "/downloads/cli/version.json") {
					fmt.Fprintf(w, `{"version":%q,"cli_build":%q}`, tc.latest, tc.latestBuild)
					return
				}
				conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer conn.Close()
				var hello localfiles.Hello
				if conn.ReadJSON(&hello) != nil || conn.WriteJSON(map[string]bool{"connected": true}) != nil {
					return
				}
				connected <- struct{}{}
				var request any
				_ = conn.ReadJSON(&request)
			}))
			defer server.Close()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var stdout, stderr bytes.Buffer
			finished := make(chan int, 1)
			go func() {
				finished <- run(ctx, []string{"--server", server.URL, "start", "--foreground"}, strings.NewReader(""), &stdout, &stderr, func(key string) string {
					if key == "AGENTWORKS_TOKEN" {
						return "test-token"
					}
					return ""
				})
			}()
			select {
			case <-connected:
			case <-time.After(10 * time.Second):
				t.Fatalf("start did not connect: %s", stderr.String())
			}
			cancel()
			<-finished
			if got := strings.Contains(stderr.String(), "agentworks update"); got != tc.hint {
				t.Fatalf("update hint = %v, want %v:\n%s", got, tc.hint, stderr.String())
			}
		})
	}
}

// A computer holds one live connection, so a share of another folder on the same device id is named before a second start tries and
// times out (PLAT-834); the same folder, and shares of another device, are not in the way.

func TestDebugReportShowsTheAgentLoginAndNotJustTheSharingOne(t *testing.T) {
	dir := t.TempDir()
	agent := filepath.Join(dir, "config.json")
	if err := agentworksclient.SaveConfig(agent, agentworksclient.Config{Server: "https://example.test", Token: "secret-token-value"}); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	server := debugLoginSection(&b, "agent login:", agent)
	missing := debugLoginSection(&b, "sharing login:", filepath.Join(dir, "executor.json"))
	text := redactDebug(b.String())
	if server != "https://example.test" || missing != "" || !strings.Contains(text, "sign-in: signed in") || !strings.Contains(text, "not set up") {
		t.Fatalf("login sections wrong (server %q, sharing server %q):\n%s", server, missing, text)
	}
	if strings.Contains(text, "secret-token-value") {
		t.Fatalf("a credential reached the report:\n%s", text)
	}
}
