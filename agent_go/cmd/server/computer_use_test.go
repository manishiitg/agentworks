package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
)

// PLAT-856: a local desktop grant never follows a shared Crew or device/agent token.
func TestComputerUseLocalOwnerAndPrivateCaptures(t *testing.T) {
	f := newMCPLaptopFixture(t)
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local")
	t.Setenv("LOCAL_MODE", "true")
	t.Setenv("MULTI_USER_MODE", "false")
	t.Setenv("DEFAULT_USER_ID", "owner")
	owner := &UserClaims{UserID: "owner", Username: "owner"}
	for name, claims := range map[string]*UserClaims{
		"owner":              owner,
		"other person":       {UserID: "other"},
		"device":             {UserID: "owner", AccessToken: &accesstokens.Token{Scopes: []string{"devices:connect"}}},
		"project connection": {UserID: "owner", AccessToken: &accesstokens.Token{ProjectKind: "work", ProjectIdentity: "project"}},
		"attached agent":     {UserID: "owner", AccessToken: &accesstokens.Token{BoundAgentID: "agent"}},
		"bot":                {UserID: "owner", Provider: "bot_owner"},
		"scoped":             {UserID: "owner", Scope: "report-preview"},
	} {
		if got := computerUseAllowed(claims); got != (name == "owner") {
			t.Errorf("%s allowed=%v", name, got)
		}
	}
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	if computerUseAllowed(owner) {
		t.Fatal("hosted server admitted desktop authority")
	}
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local")
	const root = "Crew/dashboard-team"
	if _, err := f.api.computerUseAction(context.Background(), owner, root, "chat-one", computerUseInput{Action: "run", Command: "snapshot"}); err == nil {
		t.Fatal("desktop access starts enabled")
	}
	if _, err := f.api.computerUseAction(context.Background(), owner, root, "chat-one", computerUseInput{Action: "enable"}); err != nil {
		t.Fatal(err)
	}
	original := computerUseExecute
	t.Cleanup(func() { computerUseExecute = original })
	var capture string
	computerUseExecute = func(ctx context.Context, session string, args []string) (json.RawMessage, error) {
		capture = args[len(args)-1]
		if err := os.WriteFile(capture, []byte("private image"), 0644); err != nil {
			return nil, err
		}
		return json.RawMessage(`{"ok":true}`), nil
	}
	if _, err := f.api.computerUseAction(context.Background(), owner, root, "chat-one", computerUseInput{Action: "run", Command: "screenshot", Args: []string{"--screen", "0"}}); err != nil {
		t.Fatal(err)
	}
	private, _ := computerUseChatDirectory(root, "chat-one")
	other, _ := computerUseChatDirectory(root, "chat-two")
	if filepath.Dir(capture) != private || private == other || strings.HasPrefix(private, f.docs+string(os.PathSeparator)) {
		t.Fatal("capture is not isolated outside shared project files")
	}
	if info, err := os.Stat(capture); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("capture permissions: %v, %v", info, err)
	}
	// The HTTP setup surface cannot be used to bypass interactive chat admission.
	request := adminRequest(http.MethodPost, "/", `{"action":"run","command":"snapshot"}`, owner, nil)
	request = mux.SetURLVars(request, map[string]string{"id": "work", "project_id": "dashboard-team"})
	response := httptest.NewRecorder()
	f.api.handleCrewComputerUse(response, request)
	if response.Code < 400 {
		t.Fatal("setup endpoint dispatched an app action")
	}
	// A personal MCP config loaded by an unattended CLI cannot become desktop authority.
	request = adminRequest(http.MethodPost, "/", "", owner, nil)
	response = httptest.NewRecorder()
	f.api.externalComputerUse(response, request, map[string]any{"crew_id": "dashboard-team", "action": "run", "command": "snapshot"})
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "interactive_chat_required") {
		t.Fatalf("personal MCP desktop run: status=%d body=%s", response.Code, response.Body.String())
	}
}

// PLAT-856: two chats share one physical desktop; Stop disables access and cancels its current command.
func TestComputerUseBusyAndStop(t *testing.T) {
	f := newMCPLaptopFixture(t)
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local")
	t.Setenv("LOCAL_MODE", "true")
	t.Setenv("MULTI_USER_MODE", "false")
	t.Setenv("DEFAULT_USER_ID", "owner")
	owner := &UserClaims{UserID: "owner", Username: "owner"}
	const root = "Crew/dashboard-team"
	if _, err := f.api.computerUseAction(context.Background(), owner, root, "one", computerUseInput{Action: "enable"}); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	done := make(chan error, 1)
	original := computerUseExecute
	t.Cleanup(func() { computerUseExecute = original })
	computerUseExecute = func(ctx context.Context, session string, args []string) (json.RawMessage, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	go func() {
		_, err := f.api.computerUseAction(context.Background(), owner, root, "one", computerUseInput{Action: "run", Command: "snapshot"})
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("action did not start")
	}
	if _, err := f.api.computerUseAction(context.Background(), owner, root, "two", computerUseInput{Action: "run", Command: "click", Args: []string{"@snapshot:e1"}}); err == nil || !strings.Contains(err.Error(), "busy") {
		t.Fatalf("second chat: %v", err)
	}
	if _, err := f.api.computerUseAction(context.Background(), owner, root, "two", computerUseInput{Action: "stop"}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("stop: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Stop did not cancel command")
	}
	settings, err := readComputerUse(root)
	if err != nil || settings.Enabled {
		t.Fatalf("Stop left access enabled: %v", err)
	}
}

func TestComputerUseRejectsSessionAndScreenshotPathOverrides(t *testing.T) {
	for _, in := range []computerUseInput{
		{Command: "screenshot", Args: []string{"/shared/output.png"}},
		{Command: "snapshot", Args: []string{"--session=other"}},
		{Command: "snapshot", Args: []string{"--trace", "/shared/trace"}},
		{Command: "snapshot", Args: []string{"--screenshot", "/shared/capture.html"}},
		{Command: "snapshot", Args: []string{"--agent-id", "other"}},
		{Command: "session", Args: []string{"use", "other"}},
	} {
		if _, err := computerUseArguments(in); err == nil {
			t.Fatalf("accepted override: %+v", in)
		}
	}
	args, err := computerUseArguments(computerUseInput{Command: "type", Args: []string{"@snapshot:e1", "literal $(text); value"}})
	if err != nil || args[len(args)-1] != "literal $(text); value" {
		t.Fatalf("literal arguments changed: %v, %v", args, err)
	}
}

// Desktop helpers need OS/display context, never platform secrets or AI credentials.
func TestComputerUseEnvironmentExcludesCredentials(t *testing.T) {
	for _, key := range []string{"SECRET_EXAMPLE", "ANTHROPIC_API_KEY", "AGENTWORKS_SERVER_TOKEN", "AGENT_DESKTOP_SESSION"} {
		t.Setenv(key, "must-not-pass")
	}
	t.Setenv("LANG", "en_US.UTF-8")
	env := computerUseEnvironment("/private/desktop-chat", "/runtime/bin/agent-desktop")
	for _, entry := range env {
		if strings.Contains(entry, "must-not-pass") {
			t.Fatalf("forwarded private environment key %s", strings.SplitN(entry, "=", 2)[0])
		}
	}
	if !strings.Contains(strings.Join(env, "\n"), "LANG=en_US.UTF-8") || !strings.Contains(strings.Join(env, "\n"), "AGENT_DESKTOP_HOME=/private/desktop-chat") {
		t.Fatal("missing desktop OS/session context")
	}
}

// PR472: Linux drops absolute folder-guard grants; the exact private capture
// directory must survive in the final native image reader's confinement policy.
func TestComputerUsePrivateCaptureGrantOnLocalLinux(t *testing.T) {
	tokenTestSetup(t)
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local")
	t.Setenv("LOCAL_MODE", "true")
	original := cliHostOS
	cliHostOS = "linux"
	t.Cleanup(func() { cliHostOS = original })
	capture, _ := computerUseChatDirectory("Crew/example", "one")
	other, _ := computerUseChatDirectory("Crew/example", "two")
	session := "computer-use-linux"
	common.SetSessionFolderGuard(session, []string{capture}, nil)
	policy := cliLandlockPolicyForSession(session, "claude-code", t.TempDir(), computerUseCaptureReadPolicy(nil, capture))
	found := false
	for _, path := range policy.WorkspaceReadPaths {
		if path == capture {
			found = true
		}
		if path == other || path == filepath.Dir(capture) {
			t.Fatal("capture grant widened to other chats")
		}
	}
	if !found {
		t.Fatal("private screenshot unavailable to confined native image reader")
	}
	if cliPolicyPath(other) != "" {
		t.Fatal("ordinary absolute host paths were opened")
	}
}
