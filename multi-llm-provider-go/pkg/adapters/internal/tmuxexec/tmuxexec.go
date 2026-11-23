// Package tmuxexec holds the tmux subprocess primitives shared by the
// interactive CLI adapters (codex, gemini, cursor). Before this package
// each adapter carried a byte-identical copy of the exec boilerplate and the
// capture-pane call; the only real variation was gemini's need to redact API
// keys from error messages, which RunCommandOutput supports via an optional
// ArgFormatter.
//
// NOTE: the claudecode adapter deliberately does NOT use this package — its
// runCommandOutput merges stdout and stderr into one buffer and returns the
// combined output, which several of its pane parsers rely on. That is a
// different contract from the stdout-only behavior here.
package tmuxexec

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// EnvDiagnosticsDir is an opt-in directory for terminal-pane snapshots saved
// when a tmux-backed provider fails. Panes may contain user content.
const EnvDiagnosticsDir = "TMUX_DIAGNOSTICS_DIR"

const (
	// DefaultScrollbackLines is the provider-side pane capture window for
	// tmux-backed coding CLIs. Keep this aligned with the app terminal route so
	// streamed snapshots and direct terminal reads expose the same scroll depth.
	DefaultScrollbackLines = 10000

	// DefaultHistoryLimit is passed to tmux as a string because set-option
	// expects argv values. It must be larger than DefaultScrollbackLines so tmux
	// retains enough history for captures and browser scrollback.
	DefaultHistoryLimit = "20000"
)

// ArgFormatter renders command args for inclusion in an error message. Adapters
// pass one to redact secrets (e.g. gemini redacts GEMINI_API_KEY=…); nil means
// the args are joined verbatim with spaces.
type ArgFormatter func(args []string) string

// RunCommandOutput runs name+args, returning captured stdout. stdout and stderr
// are captured separately; on failure the error includes the (optionally
// redacted) command and trimmed stderr, and the partial stdout is still
// returned. This matches the prior per-adapter runXxxCommandOutput contract.
func RunCommandOutput(ctx context.Context, stdin io.Reader, fmtArgs ArgFormatter, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if stdin != nil {
		cmd.Stdin = stdin
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		argStr := strings.Join(args, " ")
		if fmtArgs != nil {
			argStr = fmtArgs(args)
		}
		return stdout.String(), fmt.Errorf("%s %s failed: %w: %s", name, argStr, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// RunCommand runs name+args and discards stdout, returning only the error.
func RunCommand(ctx context.Context, stdin io.Reader, fmtArgs ArgFormatter, name string, args ...string) error {
	_, err := RunCommandOutput(ctx, stdin, fmtArgs, name, args...)
	return err
}

// CapturePane captures the visible pane plus `scrollbackLines` of scrollback for
// the given tmux session, joining wrapped lines (-J). scrollbackLines<=0
// captures only the visible region. Escape sequences are NOT preserved — use
// CapturePaneANSI when colors/SGR are needed (e.g. for display streaming).
func CapturePane(ctx context.Context, sessionName string, scrollbackLines int) (string, error) {
	return capturePane(ctx, sessionName, scrollbackLines, false)
}

// CapturePaneANSI is CapturePane with -e, preserving escape sequences so the
// captured text keeps its SGR color codes.
func CapturePaneANSI(ctx context.Context, sessionName string, scrollbackLines int) (string, error) {
	return capturePane(ctx, sessionName, scrollbackLines, true)
}

// WithFailureDiagnostic captures a provider's tmux pane before it is cleaned
// up and appends the artifact path to the original error. Error identity is
// retained via %w so callers can still use errors.Is/As.
func WithFailureDiagnostic(err error, provider, sessionName string) error {
	if err == nil || strings.TrimSpace(os.Getenv(EnvDiagnosticsDir)) == "" || strings.TrimSpace(sessionName) == "" {
		return err
	}
	pane, captureErr := CapturePane(context.Background(), sessionName, DefaultScrollbackLines)
	if captureErr != nil || strings.TrimSpace(pane) == "" {
		return err
	}
	dir := strings.TrimSpace(os.Getenv(EnvDiagnosticsDir))
	if mkdirErr := os.MkdirAll(dir, 0o700); mkdirErr != nil {
		// Only err is wrapped (%w) so callers can errors.Is/As on it; mkdirErr
		// is secondary diagnostic context, not part of the error chain.
		return fmt.Errorf("%w; could not save tmux diagnostic: %v", err, mkdirErr) //nolint:errorlint
	}
	file := fmt.Sprintf("%s-tmux-failure-%s-%d.txt", safeFilePart(provider), safeFilePart(sessionName), time.Now().UnixNano())
	path := filepath.Join(dir, file)
	if writeErr := os.WriteFile(path, []byte(pane), 0o600); writeErr != nil {
		// Same rationale: err stays the wrapped identity, writeErr is context only.
		return fmt.Errorf("%w; could not save tmux diagnostic: %v", err, writeErr) //nolint:errorlint
	}
	return fmt.Errorf("%w; full tmux pane saved to %s", err, path)
}

func safeFilePart(value string) string {
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			return r
		}
		return '-'
	}, strings.TrimSpace(value))
}

func capturePane(ctx context.Context, sessionName string, scrollbackLines int, preserveEscapes bool) (string, error) {
	args := []string{"capture-pane", "-p"}
	if preserveEscapes {
		args = append(args, "-e")
	}
	args = append(args, "-J")
	if scrollbackLines > 0 {
		args = append(args, "-S", "-"+strconv.Itoa(scrollbackLines))
	}
	args = append(args, "-t", sessionName)
	return RunCommandOutput(ctx, nil, nil, "tmux", args...)
}
