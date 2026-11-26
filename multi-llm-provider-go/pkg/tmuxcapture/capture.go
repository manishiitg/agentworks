package tmuxcapture

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	DefaultAgentTailLines = 80
	DefaultAgentTailBytes = 4000
	MaxCaptureLines       = 20000
	MaxAgentTailBytes     = 64 * 1024
)

var ansiEscapePattern = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)

type Options struct {
	Lines    int
	MaxBytes int
}

type Snapshot struct {
	PaneContent  string
	Text         string
	Truncated    bool
	CapturedAt   time.Time
	CaptureLines int
	RawLines     int
	RawBytes     int
}

type OutputRunner func(context.Context, ...string) (string, error)

type Capturer struct {
	Run OutputRunner
}

// CaptureAgentTail captures current tmux scrollback and returns both the
// ANSI-preserving pane content and a bounded plain-text tail suitable for an
// LLM-facing progress response.
func (c Capturer) CaptureAgentTail(ctx context.Context, tmuxSession string, options Options) (Snapshot, error) {
	tmuxSession = strings.TrimSpace(tmuxSession)
	if tmuxSession == "" {
		return Snapshot{}, fmt.Errorf("tmux session is required")
	}
	lines := normalizeLines(options.Lines)
	maxBytes := normalizeMaxBytes(options.MaxBytes)
	runner := c.Run
	if runner == nil {
		runner = runTmuxOutput
	}
	output, err := runner(ctx,
		"capture-pane", "-p", "-e", "-J", "-t", tmuxSession,
		"-S", fmt.Sprintf("-%d", lines),
	)
	if err != nil {
		return Snapshot{}, fmt.Errorf("capture tmux pane: %w", err)
	}
	paneContent := CollapseBlankRuns(output)
	text, truncated := CleanAgentTail(paneContent, maxBytes)
	return Snapshot{
		PaneContent:  paneContent,
		Text:         text,
		Truncated:    truncated,
		CapturedAt:   time.Now().UTC(),
		CaptureLines: lines,
		RawLines:     lineCount(output),
		RawBytes:     len(output),
	}, nil
}

// CleanAgentTail converts captured terminal content into compact model-facing
// text while preserving its newest complete UTF-8 lines.
func CleanAgentTail(content string, maxBytes int) (string, bool) {
	content = ansiEscapePattern.ReplaceAllString(content, "")
	lines := strings.Split(content, "\n")
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return terminalTail(strings.Join(lines, "\n"), normalizeMaxBytes(maxBytes))
}

func normalizeLines(lines int) int {
	if lines <= 0 {
		return DefaultAgentTailLines
	}
	if lines > MaxCaptureLines {
		return MaxCaptureLines
	}
	return lines
}

func normalizeMaxBytes(maxBytes int) int {
	if maxBytes <= 0 {
		return DefaultAgentTailBytes
	}
	if maxBytes > MaxAgentTailBytes {
		return MaxAgentTailBytes
	}
	return maxBytes
}

func runTmuxOutput(ctx context.Context, args ...string) (string, error) {
	output, err := exec.CommandContext(ctx, "tmux", args...).CombinedOutput()
	if err == nil {
		return string(output), nil
	}
	detail := strings.TrimSpace(string(output))
	if detail == "" {
		return "", fmt.Errorf("tmux %s: %w", strings.Join(args, " "), err)
	}
	return "", fmt.Errorf("tmux %s: %w: %s", strings.Join(args, " "), err, detail)
}

func terminalTail(content string, maxBytes int) (string, bool) {
	if len(content) <= maxBytes {
		return content, false
	}
	start := len(content) - maxBytes
	for start < len(content) && !utf8.RuneStart(content[start]) {
		start++
	}
	tail := trimLeadingPartialTerminalControl(content[start:])
	if newline := strings.IndexByte(tail, '\n'); newline >= 0 && newline < maxBytes {
		tail = tail[newline+1:]
	}
	return "[terminal output truncated; showing latest output]\n" + tail, true
}

func trimLeadingPartialTerminalControl(content string) string {
	if bel := strings.IndexByte(content, '\a'); bel >= 0 && bel < 1024 {
		if esc := strings.IndexByte(content[:bel], '\x1b'); esc < 0 {
			return content[bel+1:]
		}
	}
	return content
}

func lineCount(content string) int {
	if content == "" {
		return 0
	}
	return strings.Count(content, "\n") + 1
}
