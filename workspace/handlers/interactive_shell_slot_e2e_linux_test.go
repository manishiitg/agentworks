//go:build linux

package handlers

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/manishiitg/coding-agent-loop/workspace/slots"
	"github.com/spf13/viper"
)

// On a host with slots, a person's shell runs as their own Linux account, with a real terminal, and cannot read the
// platform's files or another person's tree. Opt-in: set AGENTWORKS_SHELL_SLOT_E2E_USER to the id of a user who holds a
// slot, AGENTWORKS_SHELL_SLOT_E2E_DOCS to the real docs folder, and run as the service account with the host's slot
// settings (AGENTWORKS_SLOTS, AGENTWORKS_SLOT_PREFIX, AGENTWORKS_SLOTCTL, AGENTWORKS_SLOTCTL_CONFIG,
// AGENTWORKS_SLOTS_FILE, AGENTWORKS_LANDLOCK_RUNNER). It only creates and removes one throwaway project folder.
func TestInteractiveShellRunsAsTheUsersSlotE2E(t *testing.T) {
	user := os.Getenv("AGENTWORKS_SHELL_SLOT_E2E_USER")
	docs := os.Getenv("AGENTWORKS_SHELL_SLOT_E2E_DOCS")
	if user == "" || docs == "" {
		t.Skip("set AGENTWORKS_SHELL_SLOT_E2E_USER and AGENTWORKS_SHELL_SLOT_E2E_DOCS to run")
	}
	slot, on, err := slots.For(user)
	if !on || err != nil || slot == "" {
		t.Fatalf("this user has no slot here (on=%v err=%v slot=%q)", on, err, slot)
	}
	gin.SetMode(gin.TestMode)
	viper.Set("docs-dir", docs)
	project := "_users/" + user + "/Chats/Code/projects/zz-shell-slot-e2e"
	abs := filepath.Join(docs, project)
	if err := os.MkdirAll(abs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(abs, 0o770|os.ModeSetgid); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("AGENTWORKS_SHELL_SLOT_E2E_KEEP") == "" {
		defer os.RemoveAll(abs)
	}

	router := gin.New()
	router.POST("/start", StartInteractiveShell)
	router.POST("/stop", StopInteractiveShell)
	router.GET("/attach", AttachInteractiveShell)
	server := httptest.NewServer(router)
	defer server.Close()
	post := func(path string, body any) int {
		raw, _ := json.Marshal(body)
		req, _ := http.NewRequest(http.MethodPost, server.URL+path, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-User-ID", user)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			t.Logf("%s answered %d: %s", path, resp.StatusCode, strings.TrimSpace(string(raw)))
		}
		return resp.StatusCode
	}
	id := "slot-e2e-" + strings.ToLower(slot)
	defer post("/stop", map[string]any{"shell_id": id})
	// Code's sandbox policy is strict (agent_go/internal/codeproduct/product.yaml), so the terminal's guard is too.
	guard := map[string]any{"enabled": true, "strict_allowlist": true, "read_paths": []string{project + "/"}, "write_paths": []string{project + "/"}}
	if code := post("/start", map[string]any{"shell_id": id, "working_directory": project, "folder_guard": guard}); code != 200 {
		t.Fatalf("start as the slot = %d", code)
	}
	// Someone without a slot's identity gets nothing: the same call without the user is refused.
	noUser, _ := http.NewRequest(http.MethodPost, server.URL+"/start", bytes.NewReader([]byte(`{"shell_id":"nobody-e2e","folder_guard":{"enabled":true,"write_paths":["x/"]}}`)))
	noUser.Header.Set("Content-Type", "application/json")
	if resp, err := http.DefaultClient.Do(noUser); err == nil {
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			t.Fatal("a shell must not start for a caller with no slot where slots are on")
		}
	}

	header := http.Header{"X-User-ID": {user}}
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/attach?shell_id="+id+"&cols=100&rows=30", header)
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	defer conn.Close()
	// The service's own .env sits two folders above the docs folder (<app>/data/docs -> <app>/.env).
	platformEnv := filepath.Join(filepath.Dir(filepath.Dir(docs)), ".env")
	script := "id -un > who.txt; tty > tty.txt 2>&1; echo \"$PS1\" > ps1.txt; cd /; cd; pwd > cdhome.txt; alias ls grep > aliases.txt 2>&1; echo \"$HOME\" > home.txt; nosuchcommand_zz > cnf.txt 2>&1; mkdir -p \"$HOME/.config/zz\" > homew.txt 2>&1 && echo writable >> homew.txt; [ -f \"$HOME/.bashrc\" ] && echo bashrc > rc.txt; " +
		"cat " + shellQuote(platformEnv) + " > env.txt 2>&1; ls " + shellQuote(filepath.Join(docs, "_users")) + " > users.txt 2>&1; " +
		"echo done > done.txt\r"
	if err := conn.WriteMessage(websocket.BinaryMessage, []byte(script)); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(abs, "done.txt")); err == nil {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	read := func(name string) string {
		raw, _ := os.ReadFile(filepath.Join(abs, name))
		return strings.TrimSpace(string(raw))
	}
	if read("done.txt") != "done" {
		t.Fatal("the shell never ran the commands; the test proves nothing")
	}
	if got := read("who.txt"); got != slot {
		t.Errorf("the shell runs as %q, want the user's slot %q", got, slot)
	}
	// The short prompt (just the folder name), not bash's user@host:/full/path.
	if ps1 := read("ps1.txt"); !strings.Contains(ps1, "${PWD##*/}") || strings.Contains(ps1, `\u@\h`) || strings.Contains(ps1, `\w\$`) {
		t.Errorf("the prompt must be the short one: %q", ps1)
	}
	// Output is coloured: the private home has no ~/.bashrc, so the shell sets the colour aliases itself.
	if aliases := read("aliases.txt"); !strings.Contains(aliases, "ls --color=auto") || !strings.Contains(aliases, "grep --color=auto") {
		t.Errorf("ls and grep must be coloured: %q", aliases)
	}
	// A mistyped command says "command not found" in plain words; Ubuntu's helper crashed here (it cannot open its database in the sandbox).
	if cnf := read("cnf.txt"); !strings.Contains(cnf, "nosuchcommand_zz: command not found") || strings.Contains(cnf, "crashed") || strings.Contains(cnf, "Traceback") {
		t.Errorf("a missing command must give the plain message: %q", cnf)
	}
	// The shell's private home is usable by the slot (it used to be owner-only, so nvm and other installers died with "Permission denied"),
	// and has a ~/.bashrc, which installers say "Profile not found" without.
	if got := read("homew.txt"); got != "writable" {
		t.Errorf("the slot cannot write in its own HOME: %q", got)
	}
	if read("rc.txt") != "bashrc" {
		t.Error("the terminal must create ~/.bashrc so installers can add themselves to it")
	}
	// An empty cd returns to the project folder the terminal started in, not the private home.
	if want, err := filepath.EvalSymlinks(abs); err == nil {
		if got := read("cdhome.txt"); got != want {
			t.Errorf("an empty cd went to %q, want the starting folder %q", got, want)
		}
	}
	if tty := read("tty.txt"); !strings.HasPrefix(tty, "/dev/pts/") {
		t.Errorf("the shell has no terminal: tty said %q", tty)
	}
	if env := read("env.txt"); !strings.Contains(env, "Permission denied") && !strings.Contains(env, "No such file") {
		t.Errorf("the shell could read the platform's .env: %.80q", env)
	}
	if users := read("users.txt"); users != "" && !strings.Contains(users, "denied") && !strings.Contains(users, "No such file") {
		t.Errorf("the shell could list everyone's folders: %.200q", users)
	}
	// HOME is the project's private home, never the service account's (a login shell read /srv/agents/home/.profile).
	if home := read("home.txt"); !strings.Contains(home, "/zz-shell-slot-e2e/.sandbox-cache/home") {
		t.Errorf("HOME = %q, want the project's private home", home)
	}
	// The service account can talk to the slot's tmux (tmux refuses other users unless granted): without that it could not
	// stop or find the shell, and shells piled up. tmux's own menus and prefix commands are off; the wheel still scrolls.
	_, socket, _ := interactiveShellPaths(id, slot)
	keys, err := exec.Command(realTmux(), "-S", socket, "list-keys").CombinedOutput()
	if err != nil || strings.Contains(string(keys), "access not allowed") {
		t.Fatalf("the service cannot reach the slot's tmux: %v %s", err, keys)
	}
	for _, menu := range []string{"MouseDown3Pane", "-T prefix"} {
		if strings.Contains(string(keys), menu) {
			t.Errorf("tmux binding %q is still on", menu)
		}
	}
	if !strings.Contains(string(keys), "WheelUpPane") {
		t.Error("the wheel binding must stay")
	}
	// Stop ends the shell: its tmux server is gone (this server's pid, not any older one on the same path).
	pidOut, _ := exec.Command(realTmux(), "-S", socket, "display-message", "-p", "#{pid}").CombinedOutput()
	serverPID := strings.TrimSpace(string(pidOut))
	if code := post("/stop", map[string]any{"shell_id": id}); code != 200 {
		t.Fatalf("stop = %d", code)
	}
	time.Sleep(700 * time.Millisecond)
	if serverPID == "" {
		t.Fatalf("could not read the tmux server pid: %s", pidOut)
	}
	if _, err := os.Stat("/proc/" + serverPID); err == nil {
		t.Errorf("tmux server %s still runs after stop", serverPID)
	}
}
