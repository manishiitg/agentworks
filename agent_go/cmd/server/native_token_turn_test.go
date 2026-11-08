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
			f, _ := newExternalCrewCommandFixture(t)
			ctx := context.Background()
			var tokenID string
			var revoke func()
			if change == "OAuth revoked" {
				store, err := openMCPOAuthStore()
				if err != nil {
					t.Fatal(err)
				}
				defer store.Close()
				grant := mcpOAuthGrant{FamilyID: "native-ask-regression", ClientID: cliOAuthClientID, Resource: "https://agentworks.example.com/api/external/v1", UserID: "other", Username: "other", Scopes: []string{"crews:read", "crews:run"}}
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
				token, _, err := store.Issue(ctx, accesstokens.Token{Name: "Ask", UserID: "other", Username: "other", AllCrews: true, Scopes: []string{"crews:read", "crews:run"}, ExpiresAt: time.Now().Add(time.Hour)}, time.Now())
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
			req := QueryRequest{AgentMode: "multi-agent", AgentProfileID: crewProfileID, AgentProfileConversationKey: "team", SelectedFolder: externalCommandCrewRoot}
			tool := f.api.bindToolExecutionContext(ctx, "native-ask", req, true)
			fresh, err := tool(context.Background(), "files")
			if err != nil || GetUserFromContext(fresh).Username != "other" {
				t.Fatalf("active connection was denied or retained stale claims: %v", err)
			}
			checkSave, err := f.api.nativeTokenTurnCheck(ctx, "native-ask", req)
			if err != nil || checkSave(ctx) != nil {
				t.Fatalf("active connection cannot save: %v", err)
			}
			// Narrowing Everyone to this named person preserves their answer.
			if err := defaultProjectOwners().SetAudience("work", "test-team", true, []string{"other"}); err != nil {
				t.Fatal(err)
			}
			if err := checkSave(ctx); err != nil {
				t.Fatalf("answer discarded after a harmless audience change: %v", err)
			}
			if change == "sharing removed" {
				if err := defaultProjectOwners().SetAudience("work", "test-team", true, nil); err != nil {
					t.Fatal(err)
				}
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
