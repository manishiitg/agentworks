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
	"github.com/manishiitg/coding-agent-loop/workspace/localfiles"
)

func TestExecutorEnablesShellForEverySharedFolder(t *testing.T) {
	for _, downloads := range []bool{false, true} {
		t.Run(fmt.Sprintf("downloads=%v", downloads), func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("APPDATA", home) // the config folder on Windows
			t.Setenv("USERPROFILE", home)
			if err := os.Mkdir(filepath.Join(home, "Downloads"), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("AGENTWORKS_LANDLOCK_RUNNER", os.Getenv("AGENTWORKS_LANDLOCK_RUNNER"))
			received := make(chan localfiles.Hello, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/external/v1/devices/connect" || r.Header.Get("Authorization") != "Bearer test-token" {
					http.Error(w, "unexpected connection", 403)
					return
				}
				conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer conn.Close()
				var hello localfiles.Hello
				if err := conn.ReadJSON(&hello); err != nil {
					return
				}
				if err := conn.WriteJSON(map[string]bool{"connected": true}); err != nil {
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
			args := []string{"--server", server.URL, "--config", filepath.Join(t.TempDir(), "executor.json"), "executor", "connect", "--device", "laptop", "--folder", "reference=" + t.TempDir(), "--write-folder", "project=" + t.TempDir()}
			if downloads {
				args = append(args, "--downloads")
			}
			go func() {
				finished <- run(ctx, args, strings.NewReader(""), &stdout, &stderr, func(key string) string {
					if key == "AGENTWORKS_TOKEN" {
						return "test-token"
					}
					return ""
				})
			}()
			select {
			case hello := <-received:
				count := 2
				if downloads {
					count++
				}
				if len(hello.Resources) != count {
					t.Fatalf("resources %+v", hello.Resources)
				}
				for _, resource := range hello.Resources {
					if !resource.Shell || resource.Writable != (resource.ID == "project" || resource.ID == "downloads") || resource.Downloads != (downloads && resource.ID != "downloads") {
						t.Fatalf("incorrect default permissions %+v", resource)
					}
				}
			case code := <-finished:
				t.Fatalf("executor exited %d: %s", code, stderr.String())
			case <-time.After(10 * time.Second):
				t.Fatal("executor did not connect")
			}
			cancel()
			select {
			case code := <-finished:
				if code != 0 {
					t.Fatalf("disconnect %d %s", code, stderr.String())
				}
				for _, message := range []string{"Sharing reference: read-only files and shell commands enabled", "Sharing project: file edits and shell commands enabled", "localhost services and your local network"} {
					if !strings.Contains(stderr.String(), message) {
						t.Fatalf("missing connection permissions %q: %s", message, stderr.String())
					}
				}
			case <-time.After(5 * time.Second):
				t.Fatal("executor did not stop")
			}
		})
	}
}

func TestExecutorRetriesAStaleDeviceConnection(t *testing.T) {
	var attempts atomic.Int32
	connected := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	args := []string{"--server", server.URL, "--config", filepath.Join(t.TempDir(), "executor.json"), "executor", "connect", "--device", "laptop", "--folder", "project=" + t.TempDir()}
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
func TestStartSharesTheCurrentFolderReadAndWrite(t *testing.T) {
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
		finished <- run(ctx, []string{"--server", server.URL, "start", "--foreground", "--workspace", "My App"}, strings.NewReader(""), &stdout, &stderr, func(key string) string {
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
		if hello.DeviceID != defaultDeviceName() || len(hello.Resources) != 1 {
			t.Fatalf("hello %+v", hello)
		}
		if r := hello.Resources[0]; r.ID != "my-app" || !r.Writable || !r.Shell {
			t.Fatalf("the folder must be shared read-and-write with shell, named after the folder: %+v", r)
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
func TestStartRequiresAWorkspace(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("APPDATA", homeDir)
	t.Setenv("USERPROFILE", homeDir)
	project := filepath.Join(t.TempDir(), "app")
	if err := os.Mkdir(project, 0700); err != nil {
		t.Fatal(err)
	}
	t.Chdir(project)
	var stdout, stderr bytes.Buffer
	code := run(t.Context(), []string{"--server", "https://agents.example.test", "start", "--foreground"}, strings.NewReader(""), &stdout, &stderr, func(key string) string {
		if key == "AGENTWORKS_TOKEN" {
			return "test-token"
		}
		return ""
	})
	if code == 0 || !strings.Contains(stderr.String(), "--workspace") {
		t.Fatalf("expected a refusal naming --workspace, got %d: %s", code, stderr.String())
	}
}

// A debug report leaves the machine (it is sent over Slack): tokens, sign-in codes and passwords must not be in it.
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
	}, "\n"))
	for _, secret := range []string{"abcdef0123456789abcdef0123456789", "aw_pat_0123", "hunter2secret", "4a6e96b1868d4506", "r-1234567890abcdef", "sk-abcdefghijklmnop"} {
		if strings.Contains(report, secret) {
			t.Fatalf("secret %q survived:\n%s", secret, report)
		}
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
				finished <- run(ctx, []string{"--server", server.URL, "start", "--foreground", "--workspace", "App"}, strings.NewReader(""), &stdout, &stderr, func(key string) string {
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
func TestOtherSharesOnDeviceNamesWhatIsInTheWay(t *testing.T) {
	running := map[string]shareState{
		"same":   {Device: "laptop", Alias: "same", Folder: "/work/same", PID: 10},
		"older":  {Device: "laptop", Alias: "older", Folder: "/work/older", PID: 11},
		"remote": {Device: "other-laptop", Alias: "x", Folder: "/work/x", PID: 12},
	}
	got := otherSharesOnDevice(running, "same", "laptop")
	if len(got) != 1 || !strings.Contains(got[0], "/work/older") || !strings.Contains(got[0], "process 11") {
		t.Fatalf("expected only the older share on this device, got %q", got)
	}
	if len(otherSharesOnDevice(running, "new", "brand-new-device")) != 0 {
		t.Fatal("a share on another device must not be reported")
	}
}
