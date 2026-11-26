package tmuxexec

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

func TestRunCommandOutputSuccess(t *testing.T) {
	out, err := RunCommandOutput(context.Background(), nil, nil, "echo", "hello", "world")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.TrimSpace(out) != "hello world" {
		t.Fatalf("stdout = %q, want %q", out, "hello world")
	}
}

func TestRunCommandOutputStdinPassedThrough(t *testing.T) {
	out, err := RunCommandOutput(context.Background(), strings.NewReader("piped-in"), nil, "cat")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "piped-in" {
		t.Fatalf("stdout = %q, want %q", out, "piped-in")
	}
}

func TestRunCommandOutputErrorIncludesStderrAndArgs(t *testing.T) {
	// `sh -c 'echo oops 1>&2; exit 3'` fails with stderr and a nonzero code.
	_, err := RunCommandOutput(context.Background(), nil, nil, "sh", "-c", "echo oops 1>&2; exit 3")
	if err == nil {
		t.Fatal("expected error for nonzero exit")
	}
	msg := err.Error()
	if !strings.Contains(msg, "oops") {
		t.Errorf("error should include stderr: %q", msg)
	}
	if !strings.Contains(msg, "sh -c") {
		t.Errorf("error should include command + args: %q", msg)
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Errorf("underlying *exec.ExitError should be unwrappable, got %T", err)
	}
}

func TestRunCommandOutputArgFormatterRedacts(t *testing.T) {
	redact := func(args []string) string {
		out := make([]string, len(args))
		for i, a := range args {
			if strings.HasPrefix(a, "SECRET=") {
				out[i] = "SECRET=<redacted>"
			} else {
				out[i] = a
			}
		}
		return strings.Join(out, " ")
	}
	_, err := RunCommandOutput(context.Background(), nil, redact, "sh", "-c", "exit 1", "SECRET=topsecret")
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "topsecret") {
		t.Errorf("redactor must keep the secret value out of the error: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "SECRET=<redacted>") {
		t.Errorf("error should show the redacted placeholder: %q", err.Error())
	}
}

func TestRunCommandReturnsErrorOnly(t *testing.T) {
	if err := RunCommand(context.Background(), nil, nil, "true"); err != nil {
		t.Fatalf("`true` should succeed: %v", err)
	}
	if err := RunCommand(context.Background(), nil, nil, "false"); err == nil {
		t.Fatal("`false` should error")
	}
}

// CapturePane / CapturePaneANSI need a live tmux; exercise them against a
// throwaway session when tmux is available.
func TestCapturePaneAgainstRealTmux(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	ctx := context.Background()
	session := "tmuxexec-test-" + randomSuffix()
	if err := RunCommand(ctx, nil, nil, "tmux", "new-session", "-d", "-s", session, "-x", "120", "-y", "40", "printf 'CAPTURE_MARKER_42'; sleep 30"); err != nil {
		t.Fatalf("failed to start tmux session: %v", err)
	}
	defer func() { _ = RunCommand(ctx, nil, nil, "tmux", "kill-session", "-t", session) }()

	var got string
	for i := 0; i < 50; i++ {
		out, err := CapturePane(ctx, session, 100)
		if err != nil {
			t.Fatalf("CapturePane error: %v", err)
		}
		if strings.Contains(out, "CAPTURE_MARKER_42") {
			got = out
			break
		}
	}
	if !strings.Contains(got, "CAPTURE_MARKER_42") {
		t.Fatalf("pane capture never showed the marker; got %q", got)
	}

	// ANSI variant must also see the marker (no escapes here, but the -e path
	// must still function).
	ansiOut, err := CapturePaneANSI(ctx, session, 100)
	if err != nil {
		t.Fatalf("CapturePaneANSI error: %v", err)
	}
	if !strings.Contains(ansiOut, "CAPTURE_MARKER_42") {
		t.Errorf("ANSI capture missing marker; got %q", ansiOut)
	}
}

func randomSuffix() string {
	return strconv.Itoa(os.Getpid())
}
