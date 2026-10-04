package handlers

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
)

// PLAT-442 step 3: normalizeOwnedPerUserDBPath through workspaceref. The right user's logical and physical spelling
// are accepted and normalize to the logical one; another user's physical path, and the bare users folder, are refused.
func TestNormalizeOwnedPerUserDBPathBothSpellings(t *testing.T) {
	gin.SetMode(gin.TestMode)
	docs := t.TempDir()
	viper.Set("docs-dir", docs)
	t.Cleanup(func() { viper.Set("docs-dir", "") })
	ctxFor := func(user string) *gin.Context {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("GET", "/", nil)
		c.Request.Header.Set("X-User-ID", user)
		return c
	}

	for _, tc := range []struct {
		name, user, path string
		want             string
		wantErr          bool
	}{
		{"own logical", "alice", "Chats/Work/db/x.sqlite", "Chats/Work/db/x.sqlite", false},
		{"own physical", "alice", "_users/alice/Chats/Work/db/x.sqlite", "Chats/Work/db/x.sqlite", false},
		{"own physical, absolute", "alice", docs + "/_users/alice/Chats/Work/db/x.sqlite", "Chats/Work/db/x.sqlite", false},
		{"another user's physical", "alice", "_users/bob/Chats/Work/db/x.sqlite", "", true},
		{"another user's physical, absolute", "alice", docs + "/_users/bob/Chats/Work/db/x.sqlite", "", true},
		{"the users folder", "alice", "_users", "", true},
		{"own physical, not a per-user folder", "alice", "_users/alice/secrets/x", "", true},
		{"shared logical path is untouched", "alice", "Workflow/w/db/x.sqlite", "Workflow/w/db/x.sqlite", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeOwnedPerUserDBPath(ctxFor(tc.user), tc.path)
			if (err != nil) != tc.wantErr || got != tc.want {
				t.Fatalf("normalize(%q as %s) = %q, %v; want %q, err=%v", tc.path, tc.user, got, err, tc.want, tc.wantErr)
			}
		})
	}
}
