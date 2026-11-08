package workspace

import (
	"encoding/json"
	"strings"
	"testing"
)

// The sizes a coding CLI actually rejected on 2026-08-04 must survive the cap.
func TestAgentShellOutputStaysUnderTheObservedRejectionSizes(t *testing.T) {
	for _, size := range []int{67930, 80857, 103685, 130046} {
		capped := capShellResultForAgent(ShellCommandResult{Stdout: strings.Repeat("x", size)})
		total := len(capped.Stdout) + len(capped.Stderr)
		if total > defaultAgentShellOutputBytes {
			t.Errorf("%d-character output capped to %d, over the %d limit", size, total, defaultAgentShellOutputBytes)
		}
	}
}

// Both ends must survive: the head says what ran, the tail says how it finished.
func TestTruncationKeepsHeadAndTail(t *testing.T) {
	body := "HEAD-MARKER" + strings.Repeat("x", 200000) + "TAIL-MARKER"
	capped := capShellResultForAgent(ShellCommandResult{Stdout: body})
	if !strings.HasPrefix(capped.Stdout, "HEAD-MARKER") {
		t.Error("head dropped")
	}
	if !strings.HasSuffix(capped.Stdout, "TAIL-MARKER") {
		t.Error("tail dropped")
	}
}

// stderr carries the reason for a failure and must not be pushed out by stdout.
func TestHugeStdoutDoesNotEvictStderr(t *testing.T) {
	capped := capShellResultForAgent(ShellCommandResult{
		Stdout:   strings.Repeat("x", 300000),
		Stderr:   "sh: line 0: cd: Workflow/build-in-public: No such file or directory\n",
		ExitCode: 1,
	})
	if !strings.Contains(capped.Stderr, "No such file or directory") {
		t.Fatalf("stderr lost: %q", capped.Stderr)
	}
	if capped.ExitCode != 1 {
		t.Errorf("exit code = %d, want 1", capped.ExitCode)
	}
}

// The budget must hold for the SERIALIZED payload, because that is what the
// consumer measures. Capping the raw streams and encoding afterwards bounded
// nothing: JSON escaping doubles quotes and backslashes and expands <, > and
// control characters sixfold, all after the check. At 48,000 capped characters
// the delivered payload reached 286,333 bytes.
func TestEncodedShellResultStaysWithinBudget(t *testing.T) {
	limit := agentShellOutputBytes()
	cases := []struct {
		name string
		unit string
	}{
		{"plain prose", "the quick brown fox jumps over it "},
		{"html report output", `<div class="row"><span>a &amp; b</span></div>`},
		{"all angle brackets", "<<<<<<<<<<"},
		{"quote heavy", `""""""""""`},
		{"backslash heavy", `\\\\\\\\\\`},
		{"control characters", "\x01\x02\x03\x04\x05"},
		{"nested json", `{"k":"v","nested":{"a":[1,2,3]},"s":"say \"hi\""}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := strings.Repeat(tc.unit, 300000/len(tc.unit))
			encoded, err := marshalCappedShellResultForAgent(ShellCommandResult{
				Stdout:  raw,
				LogFile: "runs/current/complete.log", LogError: "", LogComplete: true,
				Stderr: strings.Repeat("e", 20000),
			})
			if err != nil {
				t.Fatalf("marshal returned error: %v", err)
			}
			if len(encoded) > limit {
				t.Fatalf("encoded payload = %d bytes, exceeds the %d-byte budget", len(encoded), limit)
			}
			// It must still be valid JSON the caller can parse.
			var back ShellCommandResult
			if err := json.Unmarshal([]byte(encoded), &back); err != nil {
				t.Fatalf("encoded payload is not valid JSON: %v", err)
			}
			if back.LogFile != "runs/current/complete.log" || !back.LogComplete {
				t.Fatalf("reply cap discarded full log: %+v", back)
			}
		})
	}
}

// A cap configured smaller than the truncation marker used to emit the whole
// marker and blow the very budget it was enforcing.
func TestTinyBudgetStillRespectsTheBudget(t *testing.T) {
	t.Setenv("SHELL_MAX_AGENT_OUTPUT_BYTES", "120")
	encoded, err := marshalCappedShellResultForAgent(ShellCommandResult{
		Stdout: strings.Repeat("x", 50000),
		Stderr: strings.Repeat("y", 5000),
	})
	if err != nil {
		t.Fatalf("marshal returned error: %v", err)
	}
	if len(encoded) > 120 {
		t.Fatalf("encoded payload = %d bytes, exceeds the configured 120-byte budget", len(encoded))
	}
}
