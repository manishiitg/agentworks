// Package agentreview implements agentic (not purely deterministic) validation
// for live coding-agent tests.
//
// Coding-CLI behavior changes with every release, and deterministic string
// assertions can pass on visibly-degraded output — e.g. a Codex build that
// streamed every assistant line twice still satisfied "contentChunks >= 2". The
// remedy is to make an actual agent LOOK at the real output and sign off.
//
// A live test records the REAL captured output to a JSON file and stamps a
// fingerprint over the output's stable SHAPE (chunk order, distinct tools — not
// random tokens). The file carries an `agent_review` block. The test then
// requires that an agent has reviewed and approved the CURRENT fingerprint. When
// a new CLI release changes the shape, the fingerprint changes and the stored
// review goes stale, forcing a fresh agent review. The agent running the tests
// (a human-in-the-loop or an autonomous coding agent) reads the JSON, judges the
// streamed output, and fills in the verdict.
package agentreview

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// Review is the agent's sign-off, filled in AFTER reading the recorded output.
type Review struct {
	// Verdict is "" (not yet reviewed), "good", or "bad".
	Verdict string `json:"verdict"`
	// ReviewedFingerprint is the output fingerprint the agent actually reviewed.
	// It must equal the record's current Fingerprint for the sign-off to count —
	// so a behavior change (new fingerprint) invalidates a stale review.
	ReviewedFingerprint string   `json:"reviewed_fingerprint"`
	Reviewer            string   `json:"reviewer"`
	Issues              []string `json:"issues"`
	Notes               string   `json:"notes"`
}

// Record is the on-disk artifact: the real output plus the agent's review.
type Record struct {
	Test        string   `json:"test"`
	Summary     string   `json:"summary"`
	Criteria    []string `json:"review_criteria"` // what the agent must verify (below)
	Output      any      `json:"output"`          // the real captured output, for the agent to read
	Fingerprint string   `json:"fingerprint"`     // hash over the stable shape (see Write)
	Review      Review   `json:"agent_review"`
}

// StreamingCriteria is the rubric an agent must check when reviewing streamed
// coding-agent output. It goes beyond "did it work" into how the output READS —
// exactly what deterministic asserts cannot judge.
var StreamingCriteria = []string{
	"no duplicated lines or chunks",
	"proper formatting — clean segmentation, no run-on/garbled/merged text, no stray control chars or terminal escape codes",
	"human-readable — reads like an assistant working, coherent and natural",
	"correct text <-> tool interleaving order (narration then the tool it describes)",
	"tool calls are the real intended tools (not leaked internal/shell noise where MCP tools were expected)",
	"the final answer is coherent and matches the work performed",
	"real work actually happened (e.g. the file was written to disk)",
	"paced for a watching human — where the record carries first_signal_ms / longest_silence_ms, " +
		"something shows up promptly and keeps updating; a long opening wait or one long mid-turn " +
		"silence reads as a hung app even when the final output is perfect",
	"actually streamed rather than delivered late — delivered_in_one_block=true, or a " +
		"clean_content_spread_ms near zero, means the text appeared all at once at the end; that is " +
		"a single late delivery wearing streaming's clothes, and the chunk COUNT alone cannot tell " +
		"the difference",
	"thinking claims match the evidence — thinking_event_count=0 means the turn surfaced NO " +
		"reasoning content, so any 'thinking' a product shows for it must be streamed reply text " +
		"under a Thinking label, not the model's reasoning; say which it is rather than assuming",
	"tool calls completed — every started tool has a matching end (tool_calls_started == " +
		"tool_calls_ended); a started-but-never-ended call leaves a spinning chip in the UI",
	"usage matches native evidence — require input/output counts when the provider surfaces them; " +
		"otherwise record usage as unavailable, never claim zero cost; note absent cache counts",
}

func reviewDir() string {
	if d := os.Getenv("MLP_AGENT_REVIEW_DIR"); d != "" {
		return d
	}
	return filepath.Join("testdata", "agent-reviews")
}

func fingerprint(shape any) string {
	b, _ := json.Marshal(shape)
	sum := sha256.Sum256(b)
	return fmt.Sprintf("%x", sum[:8])
}

// Write persists the record for `test`, PRESERVING any existing agent_review
// block, and stamps the fingerprint computed over `shape` (the stable,
// token-independent shape of the output — typically the chunk order + distinct
// tool names). `output` is the full real output for a human/agent to read.
func Write(t testing.TB, test, summary string, output, shape any) Record {
	return WriteWithCriteria(t, test, summary, StreamingCriteria, output, shape)
}

// WriteWithCriteria records a live-test artifact using the rubric appropriate
// to that test surface rather than the default streaming-output rubric.
func WriteWithCriteria(t testing.TB, test, summary string, criteria []string, output, shape any) Record {
	t.Helper()
	rec := Record{Test: test, Summary: summary, Criteria: criteria, Output: output, Fingerprint: fingerprint(shape)}

	// In capture mode every run RESETS the review to pending, so a fresh suite
	// run always starts "unreviewed" and the agent running the tests must sign
	// off again. Outside capture mode the prior sign-off is carried forward (so a
	// re-run doesn't lose an approval that still matches the fingerprint).
	path := filepath.Join(reviewDir(), test+".json")
	if os.Getenv("MLP_AGENT_REVIEW_CAPTURE") == "" {
		if existing, err := os.ReadFile(path); err == nil { //nolint:gosec // G304: controlled review-record path
			var prev Record
			if json.Unmarshal(existing, &prev) == nil {
				rec.Review = prev.Review
			}
		}
	}
	if err := os.MkdirAll(reviewDir(), 0o750); err != nil {
		t.Fatalf("agentreview: mkdir %s: %v", reviewDir(), err)
	}
	b, _ := json.MarshalIndent(rec, "", "  ")
	if err := os.WriteFile(path, append(b, '\n'), 0o600); err != nil {
		t.Fatalf("agentreview: write %s: %v", path, err)
	}
	return rec
}

// RequireAllApproved scans dir for *.json review records and fails the test
// unless EVERY record is agent-approved for its current fingerprint. This is the
// cheap enforcement gate (no live CLI) run by the agentic test script AFTER an
// agent has reviewed the captured output. An empty dir passes (nothing captured
// yet). It also fails if a record is present but its verdict is "bad".
func RequireAllApproved(t testing.TB, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		t.Fatalf("agentreview: read dir %s: %v", dir, err)
	}
	var unapproved []string
	seen := 0
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		seen++
		b, err := os.ReadFile(filepath.Join(dir, e.Name())) //nolint:gosec // G304: controlled review-record path
		if err != nil {
			t.Fatalf("agentreview: read %s: %v", e.Name(), err)
		}
		var rec Record
		if json.Unmarshal(b, &rec) != nil {
			unapproved = append(unapproved, e.Name()+" (unparseable)")
			continue
		}
		if rec.Review.Verdict != "good" || rec.Review.ReviewedFingerprint != rec.Fingerprint {
			unapproved = append(unapproved, fmt.Sprintf("%s (verdict=%q fingerprint=%s reviewed=%s)",
				e.Name(), rec.Review.Verdict, rec.Fingerprint, rec.Review.ReviewedFingerprint))
		}
	}
	if len(unapproved) > 0 {
		t.Fatalf("agentreview: %d/%d records are NOT agent-approved in %s:\n  %s\n\n"+
			"An agent running the tests must open each JSON, read `output`, check it against `review_criteria`\n"+
			"(no duplication, proper formatting, human-readable, correct interleaving, real work), and set\n"+
			"agent_review.verdict=\"good\" with reviewed_fingerprint = the record's fingerprint. Then re-run the gate.",
			len(unapproved), seen, dir, joinLines(unapproved))
	}
}

func joinLines(xs []string) string {
	out := ""
	for i, x := range xs {
		if i > 0 {
			out += "\n  "
		}
		out += x
	}
	return out
}

// RequireReviewed fails the test unless an agent has reviewed and approved the
// CURRENT output shape (verdict "good" for the current fingerprint).
//
// In capture mode (MLP_AGENT_REVIEW_CAPTURE=1) it does not gate — it only records
// the output so an agent can review it, then the agent re-runs to enforce.
func RequireReviewed(t testing.TB, rec Record) {
	t.Helper()
	if os.Getenv("MLP_AGENT_REVIEW_CAPTURE") != "" {
		t.Logf("agentreview: capture mode — recorded %s (fingerprint %s); an agent must review %s.json before gating",
			rec.Test, rec.Fingerprint, rec.Test)
		return
	}
	if rec.Review.Verdict == "good" && rec.Review.ReviewedFingerprint == rec.Fingerprint {
		return
	}
	t.Fatalf("agentreview: %s output (fingerprint %s) is not agent-approved (verdict %q, reviewed_fingerprint %q).\n"+
		"An agent must open %s/%s.json, read the streamed output, judge its quality, and set\n"+
		"  agent_review.verdict = \"good\" (or \"bad\" with issues)\n"+
		"  agent_review.reviewed_fingerprint = %q\n"+
		"Re-capture the current output with MLP_AGENT_REVIEW_CAPTURE=1.",
		rec.Test, rec.Fingerprint, rec.Review.Verdict, rec.Review.ReviewedFingerprint,
		reviewDir(), rec.Test, rec.Fingerprint)
}
