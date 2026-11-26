package codexcli

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// Production 2026-10-15: a Codex launch that sat on its sign-in screen waited up to 90 minutes for a prompt that
// could never be accepted. The launch wait now ends at once with the plain login error.
func TestLaunchPromptWaitEndsOnTheSignInScreen(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	wait := codexSubmissionWait{capture: func(context.Context, string) (string, error) { return codexSignInScreen, nil }}
	start := time.Now()
	_, err := waitForCodexInitialPromptAcceptedWith(ctx, "scratch-session", "hello", nil, false, wait)
	var loginRequired *CodexLoginRequiredError
	if !errors.As(err, &loginRequired) {
		t.Fatalf("err = %v, want the sign-in error", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("the wait must end at once, took %s", time.Since(start))
	}
}

// A launch that never starts ends at the cap with plain words and a way forward (15 minutes by default).
func TestLaunchPromptWaitCapMessageIsPlain(t *testing.T) {
	t.Setenv(EnvCodexInteractivePromptMaxWaitSeconds, "1")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	wait := codexSubmissionWait{capture: func(context.Context, string) (string, error) { return "", nil }}
	_, err := waitForCodexInitialPromptAcceptedWith(ctx, "scratch-session", "hello", nil, false, wait)
	if err == nil || !strings.Contains(err.Error(), "Codex did not start in 1s. Your message is saved; press Retry.") {
		t.Fatalf("err = %v", err)
	}
}
