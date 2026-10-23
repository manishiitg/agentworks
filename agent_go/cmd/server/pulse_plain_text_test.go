package server

import (
	"strings"
	"testing"
)

// Real reason text from salesoutreach's 2026-09-25 Goal Work pass: the kind of
// text users could not follow.
const jargonReason = "Both gate-flagged issues have independently resolved (send drain PUL-3227B4D8 is external_action_required/platform-scoped with a proven workaround already running on 2 of 4 schedules)."

func TestPulsePlainTextRejectsIDsCodeTermsAndLength(t *testing.T) {
	err := checkPulsePlainText("review_note", plainTextField{name: "reason", text: jargonReason, maxLen: 600})
	if err == nil {
		t.Fatal("jargon reason was accepted")
	}
	for _, want := range []string{"PUL-3227B4D8", "external_action_required", "plain language", "review_note"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("rejection should mention %q: %v", want, err)
		}
	}
	long := strings.Repeat("Sends work again. ", 40)
	if err := checkPulsePlainText("review_note", plainTextField{name: "reason", text: long, maxLen: 600}); err == nil || !strings.Contains(err.Error(), "keep it under 600") {
		t.Fatalf("an overlong reason should be rejected for length, got %v", err)
	}
}

func TestPulsePlainTextAcceptsPlainWordsPathsAndProofDetail(t *testing.T) {
	plain := "Sends are working again in 6 of 12 groups. The Shopify segment still finds no qualified leads; I found a fix and need your OK."
	if err := checkPulsePlainText("review_note", plainTextField{name: "reason", text: plain, maxLen: 600}); err != nil {
		t.Fatalf("plain text was rejected: %v", err)
	}
	withPath := "Drafted replies in pulse/work/2026-09-25/reply_drafts.md and see https://example.com/some_page."
	if err := checkPulsePlainText("links", plainTextField{name: "action_taken", text: withPath, maxLen: 500}); err != nil {
		t.Fatalf("file paths and URLs should not count as code terms: %v", err)
	}
	proof := "Own data: outreach_activity shows 0 replies in shopify groups over 10 days (n=240)."
	if err := checkPulsePlainText("links", plainTextField{name: "detail", text: proof, maxLen: 1500, idsOnly: true}); err != nil {
		t.Fatalf("a proof field may name tables: %v", err)
	}
	if err := checkPulsePlainText("links", plainTextField{name: "detail", text: "See PUL-3227B4D8", idsOnly: true}); err == nil {
		t.Fatal("a proof field must still not carry issue IDs")
	}
}

func TestPulseDecisionSourcesAreTheReviewModules(t *testing.T) {
	for _, source := range []string{"strategic_review", "technical_review", "architecture_review", "plan_drift_review"} {
		if !isPulseDecisionSource(source) {
			t.Errorf("%s decisions should be checked", source)
		}
	}
	if isPulseDecisionSource("workflow_step") || isPulseDecisionSource("") {
		t.Error("non-Autopilot decisions must not be checked")
	}
}
