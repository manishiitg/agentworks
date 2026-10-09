package server

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"
)

func TestExternalBuilderOAuthQueuedTurnRestoresActiveFamily(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", filepath.Join(t.TempDir(), "state"))
	t.Setenv("WORKSPACE_DOCS_PATH", filepath.Join(t.TempDir(), "docs"))
	t.Setenv("MULTI_USER_MODE", "false")
	t.Setenv("AUTH_SECRET", "builder-review-local-test-secret")
	store, err := openMCPOAuthStore()
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	grant := mcpOAuthGrant{FamilyID: "review-family", ClientID: cliOAuthClientID, UserID: GetDefaultUserID(), Username: "owner", Scopes: []string{"workflows:read", "runs:execute"}}
	tx, err := store.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = issueMCPOAuthPair(t.Context(), tx, &grant)
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err = store.ActiveFamily(t.Context(), grant.FamilyID); err != nil {
		t.Fatal(err)
	}
	claims, err := accessTokenClaims(mcpOAuthTokenForGrant(grant))
	if err != nil {
		t.Fatal(err)
	}
	original := context.WithValue(t.Context(), UserContextKey, claims)
	turn := queuedConversationTurn{UserID: claims.UserID, SessionID: "review-session", Principal: queuedPrincipalFromContext(original)}
	_, err = (&StreamingAPI{}).queuedConversationTurnContext(turn, map[string]interface{}{})
	if err != nil {
		t.Fatalf("active OAuth family failed queued-turn restoration: %v", err)
	}
}

func TestExternalMCPDispatchAcceptsSuccessfulStatuses(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusAccepted, http.StatusNoContent, http.StatusForbidden, http.StatusInternalServerError} {
		recorder := &externalMCPRecorder{header: http.Header{}}
		recorder.WriteHeader(status)
		if status != http.StatusNoContent {
			_, _ = recorder.Write([]byte(`{"status":"accepted"}`))
		}
		result := externalMCPDispatchResult(recorder)
		if result.IsError != (status >= 400) {
			t.Fatalf("HTTP %d MCP error=%v", status, result.IsError)
		}
	}
}
func TestExternalBuilderDeniesIndirectRuntimeEscapes(t *testing.T) {
	c := &UserClaims{ExternalBuilderOperationID: "trusted-op"}
	for _, name := range []string{"read_image", "review_step_code", "get_workflow_config", "preview_report", "execute_shell_command", "diff_patch_workspace_file", "run_in_background", "manage_user_access", "add_mcp_server", "arbitrary_connected_tool"} {
		if !externalBuilderToolDenied(c, name) {
			t.Fatalf("indirect authority escape admitted: %s", name)
		}
	}
	for _, name := range []string{"read_file", "write_file", "list_files", "search_files", "create_plan", "update_step", "human_feedback"} {
		if externalBuilderToolDenied(c, name) {
			t.Fatalf("managed editing missing: %s", name)
		}
	}
}
