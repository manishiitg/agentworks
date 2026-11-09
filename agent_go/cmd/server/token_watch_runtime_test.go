package server

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
)

// PLAT-746-512: a real timed-out workspace lookup must not kill a running
// process, while confirmed revocation still cancels this token's runs.
func TestTokenWatcherKeepsRunningProcessOnLookupTimeoutAndStopsRevokedToken(t *testing.T) {
	tokenTestSetup(t)
	f := newExternalToolsFixture(t)
	store, err := openAccessTokens()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now()
	token, _, err := store.Issue(t.Context(), accesstokens.Token{Name: "Runtime", UserID: "owner", Username: "owner", AllWorkflows: true, Scopes: []string{"workflows:read", "files:read"}, ExpiresAt: now.Add(time.Hour)}, now)
	if err != nil {
		t.Fatal(err)
	}
	entry := accessTokenSession{token.ID, "pat-runtime", "Workflow/invoices", WorkflowAccessOwner}
	runCtx, stop := context.WithCancel(context.Background())
	defer stop()
	process := exec.CommandContext(runCtx, "python3", "-u", "-c", "import time; print('ready',flush=True); time.sleep(60)")
	stdout, err := process.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = process.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- process.Wait() }()
	t.Cleanup(func() {
		stop()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			_ = process.Process.Kill()
		}
	})
	scanner := bufio.NewScanner(stdout)
	if !scanner.Scan() || scanner.Text() != "ready" {
		t.Fatal("real process did not start")
	}
	unrelatedCtx, unrelatedStop := context.WithCancel(context.Background())
	defer unrelatedStop()
	f.api.stoppedSessions = map[string]bool{}
	f.api.sessionBusy = map[string]bool{}
	f.api.agentCancelFuncs = map[string]context.CancelFunc{entry.sessionID: stop, "other-token-run": unrelatedStop}
	f.api.accessTokenSessions.sessions = map[string]accessTokenSession{entry.sessionID: entry, "other-token-run": {"other-token", "other-token-run", "Workflow/secret", WorkflowAccessOwner}}
	if f.api.recheckAccessTokenSession(t.Context(), entry) {
		t.Fatal("active token stopped")
	}
	workspaceURL := os.Getenv("WORKSPACE_API_URL")
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	t.Setenv("WORKSPACE_API_URL", slow.URL)
	lookupCtx, expire := context.WithTimeout(t.Context(), 100*time.Millisecond)
	ended := f.api.recheckAccessTokenSession(lookupCtx, entry)
	expire()
	t.Setenv("WORKSPACE_API_URL", workspaceURL)
	slow.Close()
	if ended || runCtx.Err() != nil {
		t.Fatal("lookup timeout canceled authorized runtime")
	}
	select {
	case err := <-done:
		t.Fatalf("process exited on timeout: %v", err)
	default:
	}
	// Actual token store revocation, read and confirmed through the same watcher.
	if err = store.Revoke(t.Context(), token.ID, "owner", time.Now()); err != nil {
		t.Fatal(err)
	}
	if !f.api.recheckAccessTokenSession(t.Context(), entry) || runCtx.Err() == nil {
		t.Fatal("confirmed revocation did not stop run")
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("revoked process completed normally")
		}
		done <- err
	case <-time.After(3 * time.Second):
		t.Fatal("revoked process still running")
	}
	if unrelatedCtx.Err() != nil {
		t.Fatal("revocation canceled another token's run")
	}
	// Codex1758 P1: revocation removed the watch entry during setup, but setup
	// subsequently installed its runtime cancel function and cleared Stop.
	lateCtx, lateStop := context.WithCancel(context.Background())
	defer lateStop()
	f.api.agentCancelFuncs[entry.sessionID] = lateStop
	f.api.clearSessionStopped(entry.sessionID)
	if !f.api.recheckAccessTokenSession(t.Context(), entry) || lateCtx.Err() == nil {
		t.Fatal("post-setup revocation missed runtime whose watch entry was already removed")
	}
}

// PLAT-746-512: access expiry refuses new calls but must not kill work already
// authorized by a sign-in with a live refresh token. Revoke or full expiry does.
func TestTokenWatcherOAuthRunningProcessOutlivesAccessUntilSignInEnds(t *testing.T) {
	for _, end := range []string{"revoked", "expired"} {
		t.Run(end, func(t *testing.T) {
			tokenTestSetup(t)
			f := newExternalToolsFixture(t)
			store, err := openMCPOAuthStore()
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			grant := mcpOAuthGrant{FamilyID: "runtime-family", ClientID: cliOAuthClientID, UserID: "owner", Username: "owner", Scopes: []string{"workflows:read", "runs:execute"}}
			tx, err := store.db.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			access, _, err := issueMCPOAuthPair(t.Context(), tx, &grant)
			if err != nil {
				_ = tx.Rollback()
				t.Fatal(err)
			}
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
			entry := accessTokenSession{"oauth-" + grant.FamilyID, "pat-oauth-runtime", "Workflow/invoices", WorkflowAccessOwner}
			if !accessTokenSessionAllowed(t.Context(), entry) {
				t.Fatal("active sign-in denied before expiry")
			}
			runCtx, stop := context.WithCancel(context.Background())
			defer stop()
			process := exec.CommandContext(runCtx, "python3", "-u", "-c", "import time; print('ready',flush=True); time.sleep(60)")
			stdout, err := process.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err = process.Start(); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- process.Wait() }()
			t.Cleanup(func() {
				stop()
				select {
				case <-done:
				case <-time.After(3 * time.Second):
					_ = process.Process.Kill()
				}
			})
			scanner := bufio.NewScanner(stdout)
			if !scanner.Scan() || scanner.Text() != "ready" {
				t.Fatal("real process did not start")
			}
			f.api.stoppedSessions = map[string]bool{}
			f.api.sessionBusy = map[string]bool{}
			f.api.agentCancelFuncs = map[string]context.CancelFunc{entry.sessionID: stop}
			f.api.accessTokenSessions.sessions = map[string]accessTokenSession{entry.sessionID: entry}
			if _, err = store.db.ExecContext(t.Context(), `UPDATE tokens SET expires_at=? WHERE family_id=? AND kind='access'`, time.Now().Unix()-1, grant.FamilyID); err != nil {
				t.Fatal(err)
			}
			if _, err = store.Authenticate(t.Context(), access); err == nil {
				t.Fatal("expired access authenticated a new call")
			}
			if accessTokenSessionAllowed(t.Context(), entry) {
				t.Fatal("expired access authorized a new function call")
			}
			if f.api.recheckAccessTokenSession(t.Context(), entry) || runCtx.Err() != nil {
				t.Fatal("live refresh sign-in lost its running process")
			}
			select {
			case err := <-done:
				t.Fatalf("process exited on access expiry: %v", err)
			default:
			}
			if end == "revoked" {
				err = store.RevokeFamily(t.Context(), grant.FamilyID, "owner")
			} else {
				_, err = store.db.ExecContext(t.Context(), `UPDATE tokens SET expires_at=? WHERE family_id=? AND kind='refresh'`, time.Now().Unix()-1, grant.FamilyID)
			}
			if err != nil {
				t.Fatal(err)
			}
			if !f.api.recheckAccessTokenSession(t.Context(), entry) || runCtx.Err() == nil {
				t.Fatal("ended sign-in did not cancel running process")
			}
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("process completed normally after sign-in ended")
				}
				done <- err
			case <-time.After(3 * time.Second):
				t.Fatal("process survived ended sign-in")
			}
		})
	}
}
