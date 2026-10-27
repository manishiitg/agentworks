package server

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// PLAT-872-64: Sign in used to populate the CLI store while SDK chats read a
// different file. Exercise the actual HTTP/PTY/status/logout paths with only
// Cursor's browser approval replaced. The SDK result contains a secret that
// must never appear in the terminal, status response or application logs.
func TestCursorSDKBrowserLoginAccountIsolation(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node required")
	}
	env := newProviderAccountsEnv(t, "")
	t.Setenv("CURSOR_CLI_TRANSPORT", "sdk")
	entry := filepath.Join(t.TempDir(), "sdk.cjs")
	script := `const fs=require('fs'),path=require('path');
 const file=path.join(require('os').homedir(),'.cursor/sdk/auth.json');
 exports.Cursor={auth:{
 login:async opts=>{if(opts.openBrowser!==false)throw Error('browser must stay closed');opts.onLoginUrl('https://example.com/cursor-login');fs.mkdirSync(path.dirname(file),{recursive:true,mode:0o700});fs.writeFileSync(file,JSON.stringify({email:'selected@example.com',apiKey:'SECRET_FIXTURE'}),{mode:0o600});return{apiKey:'SECRET_FIXTURE'};},
 status:async()=>fs.existsSync(file)?{status:'logged-in',email:JSON.parse(fs.readFileSync(file)).email}:{status:'logged-out'},
 logout:async()=>fs.rmSync(file,{force:true})},models:{list:async()=>{if(!fs.existsSync(file))throw Error('not signed in');return[{id:'test-model',displayName:'Test Model',variants:[{displayName:'Test Model High',params:[{id:'effort',value:'high'}]}]}];}},me:async()=>{throw Error('SECRET_REJECTED_KEY')}};`
	if err := os.WriteFile(entry, []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CURSOR_SDK_PATH", entry)
	alice := env.addAccount(t, "alice", map[string]interface{}{"provider": "cursor-cli", "display_name": "Cursor", "auth_method": "cli_login"})
	bob := env.addAccount(t, "bob", map[string]interface{}{"provider": "cursor-cli", "display_name": "Cursor", "auth_method": "cli_login"})
	status := func(user, id string) *providerAccountStatus {
		t.Helper()
		w := env.do(t, env.api.handleProviderAccountStatus, http.MethodGet, "/status", user, nil, map[string]string{"connectionID": id})
		if w.Code != 200 || strings.Contains(w.Body.String(), "SECRET") {
			t.Fatalf("status: %d %s", w.Code, w.Body.String())
		}
		var value providerAccountStatus
		if err := json.Unmarshal(w.Body.Bytes(), &value); err != nil {
			t.Fatal(err)
		}
		return &value
	}
	home, _ := providerConnectionHome(alice.ID)
	cliLogin := filepath.Join(home, ".config/cursor/auth.json")
	if err := os.MkdirAll(filepath.Dir(cliLogin), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cliLogin, []byte(`{"accessToken":"OLD_CLI_FIXTURE"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if got := status("alice", alice.ID); got.State != "signed_out" || got.Detail == "" {
		t.Fatalf("missing SDK login: %+v", got)
	}
	logs := captureLogs(t)
	body := map[string]interface{}{"provider": "cursor-cli", "action": "authenticate", "connection_id": alice.ID}
	denied := env.do(t, env.api.handleStartProviderSetup, http.MethodPost, "/setup", "bob", body, nil)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("other user started login: %d", denied.Code)
	}
	started := env.do(t, env.api.handleStartProviderSetup, http.MethodPost, "/setup", "alice", body, nil)
	if started.Code != http.StatusCreated && started.Code != http.StatusOK {
		t.Fatalf("start login: %d %s", started.Code, started.Body.String())
	}
	var response struct {
		Session providerSetupSnapshot `json:"session"`
	}
	if err := json.Unmarshal(started.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	session, ok := env.api.providerSetupManager().get(response.Session.ID)
	if !ok {
		t.Fatalf("missing setup session: %s", started.Body.String())
	}
	t.Cleanup(func() { closeProviderSetups(env.api) })
	select {
	case <-session.done:
	case <-time.After(15 * time.Second):
		t.Fatal("login did not finish")
	}
	out := session.outputText()
	if session.snapshot().Status != "completed" || !strings.Contains(out, "https://example.com/cursor-login") || strings.Contains(out, "SECRET") {
		t.Fatalf("login terminal: %+v %s", session.snapshot(), out)
	}
	denied = env.do(t, env.api.handleProviderSetupSession, http.MethodGet, "/setup", "bob", nil, map[string]string{"id": session.id})
	if denied.Code != http.StatusNotFound {
		t.Fatalf("other user accessed login: %d", denied.Code)
	}
	denied = env.do(t, env.api.handleProviderSetupSession, http.MethodGet, "/setup", "admin", nil, map[string]string{"id": session.id})
	if denied.Code != http.StatusNotFound {
		t.Fatalf("admin accessed another person's login URL: %d", denied.Code)
	}
	if got := status("alice", alice.ID); got.State != "signed_in" || got.Identity != "selected@example.com" {
		t.Fatalf("SDK login ignored: %+v", got)
	}
	if got := status("bob", bob.ID); got.State != "signed_out" {
		t.Fatalf("cross-account login: %+v", got)
	}
	target, err := env.api.resolveProviderAccountTarget(t.Context(), alice.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	spec, err := cursorAccountCommand(t.Context(), "models")
	if err != nil {
		t.Fatal(err)
	}
	models, err := runProviderAccountCommand(t.Context(), target, spec, 15*time.Second)
	if err != nil || !strings.Contains(models, "test-model[effort=high]") || strings.Contains(models, "SECRET") {
		t.Fatalf("SDK models: %v %s", err, models)
	}
	target.Env = append(target.Env, "CURSOR_API_KEY=SECRET_REJECTED_KEY")
	if got := checkProviderAccountStatus(t.Context(), target, false); got.State != "key_rejected" {
		t.Fatalf("invalid key fell back to login: %+v", got)
	}
	loggedOut := env.do(t, env.api.handleProviderAccountSignOut, http.MethodPost, "/logout", "alice", nil, map[string]string{"connectionID": alice.ID})
	if loggedOut.Code != 200 {
		t.Fatalf("logout: %d %s", loggedOut.Code, loggedOut.Body.String())
	}
	if got := status("alice", alice.ID); got.State != "signed_out" {
		t.Fatalf("logout left login: %+v", got)
	}
	if (strings.Contains(logs.String(), "SECRET_FIXTURE") || strings.Contains(logs.String(), "SECRET_REJECTED_KEY")) || strings.Contains(logs.String(), "https://example.com/cursor-login") {
		t.Fatal("login credential or URL leaked to logs")
	}
	// Explicit CLI transports keep their own login commands.
	for _, transport := range []string{"acp", "tmux"} {
		t.Setenv("CURSOR_CLI_TRANSPORT", transport)
		spec, err := cursorAccountCommand(t.Context(), "login")
		if err != nil || spec.command != "cursor-agent" {
			t.Fatalf("explicit %s changed: %v %+v", transport, err, spec)
		}
	}
}
