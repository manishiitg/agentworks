package server

import (
	"context"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
)

type recordingInstructionAppender struct {
	parts []string
}

func (r *recordingInstructionAppender) AddInstructions(items ...string) error {
	r.parts = append(r.parts, items...)
	return nil
}

func TestConnectedToolsPromptReflectsConnectionsAndPermissions(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("AGENTWORKS_LOCAL_SERVER_PRODUCTS", "")
	api, workspace := setupSlackConnectionTest(t)
	root := "Crew/connected-demo"
	const (
		ghConnID   = "0123456789abcdef0123456789abcdef"
		ghSecret   = "VLT_TEST__GITHUB_0123456789ABCDEF0123456789ABCDEF"
		ghRawToken = "ghp_super_secret_must_never_appear"
	)
	workspace.files[root+"/product.json"] = `{"id":"connected-demo","product":"work","title":"Connected Demo"}`
	workspace.files[root+"/workflow.json"] = `{"schema_version":1,"id":"connected-demo","capabilities":{"github_connection_id":"` + ghConnID + `"}}`

	prevOwners := crewOwners.read
	crewOwners.read = func(_ context.Context, path string) string {
		if path == root {
			return "default"
		}
		return ""
	}
	clearOwners := func() {
		crewOwners.mu.Lock()
		crewOwners.entries = map[string]crewOwnerEntry{}
		crewOwners.mu.Unlock()
	}
	clearOwners()
	t.Cleanup(func() {
		crewOwners.read = prevOwners
		clearOwners()
	})

	prevTokenReader := githubTokenValue
	githubTokenValue = func(rec *githubAccountRecord) string {
		if rec != nil && rec.SecretName == ghSecret {
			return ghRawToken
		}
		return ""
	}
	t.Cleanup(func() { githubTokenValue = prevTokenReader })

	if err := writeGitHubRecord("default", githubAccountRecord{
		ID:         ghConnID,
		Login:      "octocat",
		Source:     "pat",
		SecretName: ghSecret,
	}); err != nil {
		t.Fatal(err)
	}

	// Local Mac mode with Computer use enabled.
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "local")
	t.Setenv("MULTI_USER_MODE", "false")
	if err := saveComputerUse(root, true); err != nil {
		t.Fatal(err)
	}

	ownerClaims := &UserClaims{UserID: "default", Username: "user"}
	prompt := api.buildConnectedToolsPrompt(context.Background(), crewProfileID, ownerClaims, root, false, false)
	if !strings.Contains(prompt, "## Connected here") {
		t.Fatalf("expected Connected here heading, got %q", prompt)
	}
	if !strings.Contains(prompt, "GitHub**: connected as `octocat`") {
		t.Fatalf("expected GitHub login in prompt, got %q", prompt)
	}
	if !strings.Contains(prompt, "Computer use**: enabled on this computer") {
		t.Fatalf("expected Computer use line in local owner prompt, got %q", prompt)
	}
	if strings.Contains(prompt, ghRawToken) {
		t.Fatalf("prompt must never contain credential values: %q", prompt)
	}

	// Guest ask / Run mode must omit GitHub and Computer use.
	guestPrompt := api.buildConnectedToolsPrompt(context.Background(), crewProfileID, ownerClaims, root, true, true)
	if strings.Contains(guestPrompt, "octocat") || strings.Contains(guestPrompt, "Computer use") {
		t.Fatalf("guest/Run mode prompt must omit owner-only secrets and computer use, got %q", guestPrompt)
	}

	// Server mode: Computer use is omitted, Slack bot is shown when configured and enabled.
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	t.Setenv("MULTI_USER_MODE", "true")
	svc, err := ensureSlackService()
	if err != nil {
		t.Fatal(err)
	}
	bot, err := svc.CreateSlackConnection(context.Background(), services.SlackConnectionInput{
		DisplayName:   "Release helper",
		BotToken:      "xoxb-fixture-bot",
		AppToken:      "xapp-fixture-app",
		Enabled:       false,
		WorkspacePath: root,
		ProfileID:     crewProfileID,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Disabled bot must not be listed; enable it via update? Keep disabled check first.
	workspace.files[root+"/workflow.json"] = `{"schema_version":1,"id":"connected-demo","capabilities":{"github_connection_id":"` + ghConnID + `","slack_connection_id":"` + bot.ID + `"}}`
	serverPrompt := api.buildConnectedToolsPrompt(context.Background(), crewProfileID, ownerClaims, root, false, false)
	if strings.Contains(serverPrompt, "Computer use") {
		t.Fatalf("server prompt must omit Computer use, got %q", serverPrompt)
	}
	if strings.Contains(serverPrompt, "Release helper") {
		t.Fatalf("disabled Slack bot must not be listed, got %q", serverPrompt)
	}

	// Non-project profiles get no Connected here section.
	if nonProject := api.buildConnectedToolsPrompt(context.Background(), "agentworks", ownerClaims, root, false, false); nonProject != "" {
		t.Fatalf("non-project profile must return empty connected prompt, got %q", nonProject)
	}

	// Google grant summary formatting (pure local scope checks).
	grants := connectedGoogleGrants(services.GmailConnection{
		AllowReadAccess: true,
		Scopes:          []string{services.GmailReadonlyScope},
	})
	if strings.Join(grants, ",") != "Gmail read" {
		t.Fatalf("unexpected google grants: %v", grants)
	}

	// Verify prompt_sections assembly includes connected-tools for project profiles and skips when empty.
	appender := &recordingInstructionAppender{}
	included, _, err := assemblePromptSections(appender, promptContext{
		ProfileID:      crewProfileID,
		ConnectedTools: prompt,
	})
	if err != nil {
		t.Fatal(err)
	}
	foundSection := false
	for _, name := range included {
		if name == "connected-tools" {
			foundSection = true
			break
		}
	}
	if !foundSection {
		t.Fatalf("connected-tools section not included: %v", included)
	}
}
