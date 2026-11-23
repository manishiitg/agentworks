package testcontracts

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/internal/agentreview"
)

func AssertCleanFinalExtraction(t testing.TB, provider, content string, wantContains, forbidden []string) {
	t.Helper()

	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		t.Fatalf("%s final extraction is empty", provider)
	}
	for _, want := range wantContains {
		if !strings.Contains(trimmed, want) {
			t.Fatalf("%s final extraction missing %q:\n%s", provider, want, trimmed)
		}
	}
	for _, bad := range forbidden {
		if strings.Contains(trimmed, bad) {
			t.Fatalf("%s final extraction leaked %q:\n%s", provider, bad, trimmed)
		}
	}
}

type FinalExtractionJudgeCase struct {
	Provider     string
	TmuxScreen   string
	Extracted    string
	UserGoal     string
	MustContain  []string
	Forbidden    []string
	ExpectedNote string
}

// AssertAgentJudgesFinalExtraction judges whether Extracted is the clean final
// assistant response pulled from TmuxScreen. It first runs the deterministic
// must-contain/forbidden gate, then hands the case to the agentreview flow: the
// real captured output is recorded to testdata/agent-reviews/<TestName>.json
// against FinalExtractionCriteria, and the test passes only if an agent has
// signed off on the CURRENT extraction fingerprint.
//
// This replaces the former external LLM (Gemini/Vertex) judge. The semantic
// judgment is unchanged — an intelligent reviewer confirms the extraction reads
// as the clean final answer, not chrome/tool noise — but there is no API key or
// network call, and the verdict is committed to the repo. For a fixture-driven
// case the extraction is deterministic, so one sign-off persists across runs;
// changing the parser changes the fingerprint and forces a fresh review.
func AssertAgentJudgesFinalExtraction(t testing.TB, c FinalExtractionJudgeCase) {
	t.Helper()
	if strings.TrimSpace(c.TmuxScreen) == "" {
		t.Fatalf("%s final extraction judge case has empty raw provider output", c.Provider)
	}
	AssertCleanFinalExtraction(t, c.Provider, c.Extracted, c.MustContain, c.Forbidden)

	output := map[string]any{
		"provider":            c.Provider,
		"user_goal":           c.UserGoal,
		"expected_note":       c.ExpectedNote,
		"must_contain":        c.MustContain,
		"forbidden":           c.Forbidden,
		"raw_provider_output": truncateForJudge(c.TmuxScreen),
		"extracted_final":     c.Extracted,
	}
	// Fingerprint over the extraction shape (provider + extracted text): stable
	// for a deterministic parser, and it changes exactly when the extraction
	// output changes — which is when a fresh agent review is genuinely needed.
	// Live tests embed a fresh random token per run (LIVE_CLAUDE_FINAL_<hex>);
	// it is normalised out so an approval carries over across runs, while any
	// change to the extracted text's content or format still invalidates it.
	shape := map[string]any{"provider": c.Provider, "extracted": liveRunTokenPattern.ReplaceAllString(c.Extracted, "<RUN_TOKEN>")}
	summary := fmt.Sprintf("Final-response extraction for %s: is EXTRACTED_FINAL the clean final answer from RAW_PROVIDER_OUTPUT?", c.Provider)

	rec := agentreview.WriteWithCriteria(t, t.Name(), summary, agentreview.FinalExtractionCriteria, output, shape)
	agentreview.RequireReviewed(t, rec)
}

// liveRunTokenPattern matches the per-run random tokens live tests embed:
// an upper-case name ending in _<8+ lower-case hex chars>.
var liveRunTokenPattern = regexp.MustCompile(`\b[A-Z][A-Z0-9_]*_[0-9a-f]{8,}\b`)

func truncateForJudge(s string) string {
	const max = 12000
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return "[truncated to last screen rows]\n" + string(runes[len(runes)-max:])
}
