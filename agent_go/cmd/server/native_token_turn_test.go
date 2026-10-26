package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
)

// PLAT-750-14: native Ask retained its initiating token snapshot after
// revocation. Use real credential stores and the production tool/save checks.
func TestNativeAskRechecksTokenAndSharingAtToolsAndFinalSave(t *testing.T) {
	for _, change := range []string{"PAT revoked", "OAuth revoked", "sharing removed"} {
		t.Run(change, func(t *testing.T) {
			tokenTestSetup(t)
			f := newExternalToolsFixture(t)
			ctx := context.Background()
			var tokenID string
			var revoke func()
			if change == "OAuth revoked" {
				store, err := openMCPOAuthStore()
				if err != nil {
					t.Fatal(err)
				}
				defer store.Close()
				grant := mcpOAuthGrant{FamilyID: "native-ask-regression", ClientID: cliOAuthClientID, Resource: "https://agentworks.example.com/api/external/v1", UserID: "reader", Username: "reader", Scopes: []string{"workflows:read", "runs:execute"}}
				tx, err := store.db.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				if _, _, err := issueMCPOAuthPair(ctx, tx, &grant); err != nil {
					_ = tx.Rollback()
					t.Fatal(err)
				}
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
				tokenID = "oauth-" + grant.FamilyID
				revoke = func() {
					if err := store.RevokeFamily(ctx, grant.FamilyID, grant.UserID); err != nil {
						t.Fatal(err)
					}
				}
			} else {
				store, err := openAccessTokens()
				if err != nil {
					t.Fatal(err)
				}
				defer store.Close()
				token, _, err := store.Issue(ctx, accesstokens.Token{Name: "Ask", UserID: "reader", Username: "reader", AllWorkflows: true, Scopes: []string{"workflows:read", "runs:execute"}, ExpiresAt: time.Now().Add(time.Hour)}, time.Now())
				if err != nil {
					t.Fatal(err)
				}
				tokenID = token.ID
				revoke = func() {
					if err := store.Revoke(ctx, token.ID, token.UserID, time.Now()); err != nil {
						t.Fatal(err)
					}
				}
			}
			claims, err := activeExternalGrantClaims(ctx, tokenID)
			if err != nil {
				t.Fatal(err)
			}
			claims.Username = "stale snapshot"
			ctx = context.WithValue(ctx, UserContextKey, claims)
			req := QueryRequest{AgentMode: "workflow_phase", SelectedFolder: "Workflow/invoices", PhaseID: "workflow-builder"}
			tool := f.api.bindToolExecutionContext(ctx, "native-ask", req, true)
			fresh, err := tool(context.Background(), "files")
			if err != nil || GetUserFromContext(fresh).Username != "reader" {
				t.Fatalf("active connection was denied or retained stale claims: %v", err)
			}
			checkSave, err := f.api.nativeTokenTurnCheck(ctx, "native-ask", req)
			if err != nil || checkSave(ctx) != nil {
				t.Fatalf("active connection cannot save: %v", err)
			}
			// Gaining Builder access must not discard an answer started in Run.
			f.write(t, "Workflow/invoices/workflow.json", `{"id":"invoices","label":"Invoices","access":{"owners":["owner","reader"]}}`)
			if err := checkSave(ctx); err != nil {
				t.Fatalf("answer discarded after a harmless access change: %v", err)
			}
			if change == "sharing removed" {
				f.write(t, "Workflow/invoices/workflow.json", `{"id":"invoices","label":"Invoices","access":{"owners":["owner"]}}`)
			} else {
				revoke()
			}
			if _, err := tool(context.Background(), "files"); err == nil {
				t.Fatal("next evidence read accepted after access was removed")
			}
			if err := checkSave(ctx); err == nil {
				t.Fatal("final transcript save accepted after access was removed")
			}
			if change != "sharing removed" {
				// Also hit native HTTP admission with the already captured claims.
				r := httptest.NewRequest(http.MethodPost, "/api/query", strings.NewReader(`{"query":"continue"}`)).WithContext(ctx)
				w := httptest.NewRecorder()
				f.api.handleQuery(w, r)
				if w.Code != http.StatusForbidden {
					t.Fatalf("revoked connection admitted a new turn: %d", w.Code)
				}
			}
		})
	}
}
