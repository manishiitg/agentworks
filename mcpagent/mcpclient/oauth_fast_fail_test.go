package mcpclient

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
	"github.com/manishiitg/mcpagent/oauth"
)

// A hosted OAuth connector the user has never signed in to cannot be fixed by
// retrying: the token file only appears after an interactive login. Both retry
// loops must return after the first attempt (an agent start on a deployment
// with one such connector used to burn ~19s here before every turn).
func TestConnectFailsFastWithoutOAuthToken(t *testing.T) {
	cfg := MCPServerConfig{
		URL: "https://127.0.0.1:1/mcp", // never reached: the token check comes first
		OAuth: &oauth.OAuthConfig{ // #nosec G101 -- Synthetic example.invalid OAuth endpoints and a temporary token-file path; no credentials.
			ClientID:  "https://example.invalid/.well-known/mcp-client.json",
			AuthURL:   "https://example.invalid/authorize",
			TokenURL:  "https://example.invalid/token",
			TokenFile: filepath.Join(t.TempDir(), "Notion.json"),
		},
	}

	for _, tc := range []struct {
		name    string
		connect func(*Client, context.Context) error
	}{
		{"Connect", func(c *Client, ctx context.Context) error { return c.Connect(ctx) }},
		{"ConnectWithRetry", func(c *Client, ctx context.Context) error { return c.ConnectWithRetry(ctx) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := New(cfg, loggerv2.NewNoop())
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			start := time.Now()
			err := tc.connect(c, ctx)
			elapsed := time.Since(start)
			if err == nil {
				t.Fatal("expected an error without a token")
			}
			if !errors.Is(err, oauth.ErrNoValidToken) {
				t.Fatalf("error should wrap oauth.ErrNoValidToken, got: %v", err)
			}
			// The inner loop alone sleeps 1s+2s between its attempts; a single
			// attempt finishes well under that.
			if elapsed > 900*time.Millisecond {
				t.Fatalf("missing-token failure retried: took %s", elapsed)
			}
		})
	}
}

// ConnectWithRetry must make exactly MaxRetries+1 attempts. It used to call
// Connect (its own 3-attempt loop), tripling every retry and stacking both
// backoffs.
func TestConnectWithRetryDoesNotNestConnectRetries(t *testing.T) {
	c := New(MCPServerConfig{Command: "/nonexistent/mlp-mcp-server-binary"}, loggerv2.NewNoop())
	c.retryConfig = RetryConfig{MaxRetries: 2, InitialDelay: time.Millisecond, MaxDelay: 2 * time.Millisecond, BackoffFactor: 2, ConnectTimeout: 5 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	start := time.Now()
	if err := c.ConnectWithRetry(ctx); err == nil {
		t.Fatal("expected a missing binary to fail")
	}
	if got := c.connectAttempts.Load(); got != 3 {
		t.Fatalf("ConnectWithRetry made %d attempts, want MaxRetries+1 = 3", got)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("took %s; backoffs are stacking", elapsed)
	}
}
