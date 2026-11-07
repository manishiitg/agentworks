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

// An agent told only "truncated" re-runs the identical command; three of the
// 2026-08-04 rejections were byte-identical repeats.
func TestTruncationMarkerForbidsAnIdenticalRetryAndNamesAlternatives(t *testing.T) {
	capped := capShellResultForAgent(ShellCommandResult{Stdout: strings.Repeat("x", 200000)})
	for _, want := range []string{"Do NOT re-run this command unchanged", "grep", "head/tail", "sed -n", "jq/awk"} {
		if !strings.Contains(capped.Stdout, want) {
			t.Errorf("marker omits %q", want)
		}
	}
	if !strings.Contains(capped.Stdout, "of 200000 characters") {
		t.Errorf("marker does not state the real size: %s", capped.Stdout[:400])
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

// Output that already fits is returned byte-for-byte.
func TestOutputUnderTheCapIsUntouched(t *testing.T) {
	original := ShellCommandResult{Stdout: "hello", Stderr: "warn", ExitCode: 0}
	if capped := capShellResultForAgent(original); capped != original {
		t.Errorf("small result modified: %#v", capped)
	}
}

func TestCapCanBeDisabledForADifferentConsumer(t *testing.T) {
	t.Setenv("SHELL_MAX_AGENT_OUTPUT_BYTES", "0")
	body := strings.Repeat("x", 200000)
	if capped := capShellResultForAgent(ShellCommandResult{Stdout: body}); capped.Stdout != body {
		t.Errorf("cap applied when disabled: %d characters", len(capped.Stdout))
	}
}

func TestCapHonoursAnOperatorOverride(t *testing.T) {
	t.Setenv("SHELL_MAX_AGENT_OUTPUT_BYTES", "1000")
	capped := capShellResultForAgent(ShellCommandResult{Stdout: strings.Repeat("x", 50000)})
	if len(capped.Stdout) > 1000 {
		t.Errorf("override ignored: %d characters", len(capped.Stdout))
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

// Output that already fits must pass through untouched — the cap should cost
// nothing in the ordinary case.
func TestSmallShellResultIsNotTruncated(t *testing.T) {
	result := ShellCommandResult{Stdout: "hello <world> & \"friends\"", Stderr: "", ExitCode: 0}
	encoded, err := marshalCappedShellResultForAgent(result)
	if err != nil {
		t.Fatalf("marshal returned error: %v", err)
	}
	var back ShellCommandResult
	if err := json.Unmarshal([]byte(encoded), &back); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	if back.Stdout != result.Stdout {
		t.Fatalf("stdout = %q, want it unchanged", back.Stdout)
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

// Disabling HTML escaping is what keeps ordinary report output from costing 6x.
// The bytes must survive the round trip unchanged.
func TestHTMLIsNotEscapedButRoundTrips(t *testing.T) {
	result := ShellCommandResult{Stdout: `<a href="x">A & B</a>`}
	encoded, err := encodeShellResultForAgent(result)
	if err != nil {
		t.Fatalf("encode returned error: %v", err)
	}
	// With escaping off the raw characters survive verbatim. Asserting their
	// presence avoids writing the six-character escape sequence as a literal,
	// which is easy to mangle and easy to misread.
	for _, ch := range []string{"<", ">", "&"} {
		if !strings.Contains(encoded, ch) {
			t.Fatalf("payload escaped %q instead of emitting it verbatim: %s", ch, encoded)
		}
	}
	var back ShellCommandResult
	if err := json.Unmarshal([]byte(encoded), &back); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	if back.Stdout != result.Stdout {
		t.Fatalf("stdout = %q, want %q", back.Stdout, result.Stdout)
	}
}

// A quoting error in generated code reads like a typo, so agents rewrite the
// same prose the same way — one observed session reached migrate3.py before
// giving up. The result must name the cause and forbid the identical retry.
func TestQuotingFailureGetsAnActionableHint(t *testing.T) {
	res := annotateKnownShellFailures(ShellCommandResult{
		ExitCode: 1,
		Stderr:   "  File \"Downloads/migrate3.py\", line 41\nSyntaxError: unterminated string literal (detected at line 41)",
	})
	for _, want := range []string{
		"escaped twice",
		"Do NOT rewrite the same text",
		"QUOTED heredoc delimiter",
		"diff_patch_workspace_file",
	} {
		if !strings.Contains(res.Stderr, want) {
			t.Fatalf("quoting hint missing %q, got: %s", want, res.Stderr)
		}
	}
	// The original error must survive; the hint is added, not substituted.
	if !strings.Contains(res.Stderr, "SyntaxError: unterminated string literal") {
		t.Fatal("hint replaced the original error instead of appending to it")
	}
}

// The hint must not fire on unrelated failures or on success, or it becomes
// noise the agent learns to skip.
func TestQuotingHintDoesNotFireSpuriously(t *testing.T) {
	cases := []ShellCommandResult{
		{ExitCode: 0, Stderr: "unterminated string literal"},                   // succeeded; not a failure
		{ExitCode: 1, Stderr: "ModuleNotFoundError: No module named 'pandas'"}, // different failure
		{ExitCode: 2, Stderr: "grep: no such file"},
	}
	for _, in := range cases {
		if got := annotateKnownShellFailures(in); got.Stderr != in.Stderr {
			t.Fatalf("hint fired on %q", in.Stderr)
		}
	}
}

// It must survive the cap: a hint truncated away helps nobody.
func TestQuotingHintSurvivesCapping(t *testing.T) {
	encoded, err := marshalCappedShellResultForAgent(ShellCommandResult{
		ExitCode: 1,
		Stdout:   strings.Repeat("noise ", 40000),
		Stderr:   "SyntaxError: unterminated string literal (detected at line 41)",
	})
	if err != nil {
		t.Fatalf("marshal returned error: %v", err)
	}
	if !strings.Contains(encoded, "escaped twice") {
		t.Fatal("quoting hint did not survive truncation")
	}
}
