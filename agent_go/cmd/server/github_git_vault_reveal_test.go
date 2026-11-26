package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
)

func TestWorkspaceGitOriginGitHubRepo(t *testing.T) {
	cases := []struct {
		name   string
		config string
		want   string
	}{
		{
			name:   "https remote with .git",
			config: "[core]\n\tbare = false\n[remote \"origin\"]\n\turl = https://github.com/acme-core/platform.git\n\tfetch = +refs/heads/*:refs/remotes/origin/*\n",
			want:   "acme-core/platform",
		},
		{
			name:   "ssh remote",
			config: "[remote \"origin\"]\n\turl = git@github.com:acme-core/deployments.git\n",
			want:   "acme-core/deployments",
		},
		{
			name:   "credential-bearing https remote strips credentials",
			config: "[remote \"origin\"]\n\turl = https://x-access-token:ghp_secret123@github.com/partner-org/portal-web.git\n",
			want:   "partner-org/portal-web",
		},
		{
			name:   "non-github remote returns empty",
			config: "[remote \"origin\"]\n\turl = https://gitlab.example.com/team/repo.git\n",
			want:   "",
		},
		{
			name:   "other remote section ignored",
			config: "[remote \"upstream\"]\n\turl = https://github.com/acme-core/upstream.git\n",
			want:   "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "config"), []byte(tc.config), 0o600); err != nil {
				t.Fatal(err)
			}
			if got := workspaceGitOriginGitHubRepo(dir); got != tc.want {
				t.Fatalf("workspaceGitOriginGitHubRepo() = %q, want %q", got, tc.want)
			}
		})
	}
	if got := workspaceGitOriginGitHubRepo(filepath.Join(t.TempDir(), "missing")); got != "" {
		t.Fatalf("missing config = %q, want empty", got)
	}
}

func TestPersonalVaultOwnerCanRevealOwnSecretOnly(t *testing.T) {
	api := managedGlobalTestAPI(t)
	ctx := context.Background()

	ownSecret, err := vaultSecretName("v-alice001", "MY_API_KEY")
	if err != nil {
		t.Fatal(err)
	}
	otherSecret, err := vaultSecretName("v-bob00002", "OTHER_KEY")
	if err != nil {
		t.Fatal(err)
	}
	if err := api.storeManagedGlobalSecret(ctx, ownSecret, "alice-own-secret-val", false); err != nil {
		t.Fatal(err)
	}
	if err := api.storeManagedGlobalSecret(ctx, otherSecret, "bob-own-secret-val", false); err != nil {
		t.Fatal(err)
	}
	if err := api.saveManagedGlobalSecret(ctx, "admin", "COMPANY_KEY", "company-secret-val", false); err != nil {
		t.Fatal(err)
	}

	token := strings.Repeat("v", 32)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path == "/api/vaults" && r.Method == http.MethodGet {
			actor := r.Header.Get("X-CapLayer-Actor")
			if actor == "a1" {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"vaults": []map[string]any{
						{"group": map[string]any{"id": "v-alice001", "name": personalVaultName}, "role": "owner", "secret_names": []string{ownSecret}},
						{"group": map[string]any{"id": "v-shared01", "name": "Shared"}, "role": "member", "secret_names": []string{otherSecret}},
					},
				})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"vaults": []any{}})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("CAPLAYER_SERVICE_URL", srv.URL)
	t.Setenv("CAPLAYER_SERVICE_TOKEN", token)
	t.Setenv("CAPLAYER_SERVICE_TOKEN_FILE", "")

	reveal := func(uid, name string, accessToken bool) *httptest.ResponseRecorder {
		req := sharedSecretsRequest(http.MethodGet, "/api/secrets/global/reveal?name="+name, uid, nil)
		if accessToken {
			claims := GetUserFromContext(req.Context())
			copyClaims := *claims
			copyClaims.AccessToken = &accesstokens.Token{ID: "pat-1"}
			req = req.WithContext(context.WithValue(req.Context(), UserContextKey, &copyClaims))
		}
		rec := httptest.NewRecorder()
		api.handleRevealGlobalSecret(rec, req)
		return rec
	}

	// Owner can reveal their own personal Vault secret.
	if rec := reveal("a1", ownSecret, false); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "alice-own-secret-val") {
		t.Fatalf("owner reveal own secret: %d %s", rec.Code, rec.Body.String())
	}
	// Owner cannot reveal a secret from a vault where they are only a member (not owner).
	if rec := reveal("a1", otherSecret, false); rec.Code != http.StatusForbidden || strings.Contains(rec.Body.String(), "bob-own-secret-val") {
		t.Fatalf("non-owner reveal member vault secret: %d %s", rec.Code, rec.Body.String())
	}
	// Non-admin still cannot reveal company global secrets.
	if rec := reveal("a1", "COMPANY_KEY", false); rec.Code != http.StatusForbidden || strings.Contains(rec.Body.String(), "company-secret-val") {
		t.Fatalf("non-admin reveal company secret: %d %s", rec.Code, rec.Body.String())
	}
	// Another user cannot reveal a1's personal secret.
	if rec := reveal("c3", ownSecret, false); rec.Code != http.StatusForbidden || strings.Contains(rec.Body.String(), "alice-own-secret-val") {
		t.Fatalf("other user reveal: %d %s", rec.Code, rec.Body.String())
	}
	// External access tokens can never reveal values, even for an admin.
	if rec := reveal("admin", "COMPANY_KEY", true); rec.Code != http.StatusForbidden || strings.Contains(rec.Body.String(), "company-secret-val") {
		t.Fatalf("access token reveal: %d %s", rec.Code, rec.Body.String())
	}
}
