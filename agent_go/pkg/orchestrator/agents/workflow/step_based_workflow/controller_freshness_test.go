package step_based_workflow

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func parseLedger(t *testing.T, js string) FreshnessLedger {
	t.Helper()
	var l FreshnessLedger
	if err := json.Unmarshal([]byte(js), &l); err != nil {
		t.Fatalf("unmarshal ledger: %v\n%s", err, js)
	}
	return l
}

func TestApplyFreshnessConfirmationStampsAndCounts(t *testing.T) {
	// First confirmation on an empty ledger — an "updated" learnings write.
	js1, err := applyFreshnessConfirmation("", "learnings", "iteration-0/groupA", "step-login", freshnessActionUpdated, "2026-07-18T10:00:00Z")
	if err != nil {
		t.Fatalf("first apply: %v", err)
	}
	l1 := parseLedger(t, js1)
	if l1.Store != "learnings" || l1.ConfirmCount != 1 || l1.UpdatedCount != 1 {
		t.Fatalf("unexpected first ledger: %+v", l1)
	}
	if l1.LastConfirmedRun != "iteration-0/groupA" || l1.LastConfirmedAt != "2026-07-18T10:00:00Z" {
		t.Fatalf("first confirmation not stamped: %+v", l1)
	}
	if len(l1.History) != 1 || l1.History[0].Action != freshnessActionUpdated || l1.History[0].Step != "step-login" {
		t.Fatalf("first history wrong: %+v", l1.History)
	}

	// Second confirmation — reviewed-unchanged; confirm_count rises but not updated_count.
	js2, err := applyFreshnessConfirmation(js1, "learnings", "iteration-0/groupA", "step-scrape", freshnessActionConfirmedUnchanged, "2026-07-18T11:00:00Z")
	if err != nil {
		t.Fatalf("second apply: %v", err)
	}
	l2 := parseLedger(t, js2)
	if l2.ConfirmCount != 2 || l2.UpdatedCount != 1 {
		t.Fatalf("second counts wrong: confirm=%d updated=%d", l2.ConfirmCount, l2.UpdatedCount)
	}
	if l2.LastConfirmedAt != "2026-07-18T11:00:00Z" {
		t.Fatalf("last_confirmed_at not advanced: %+v", l2)
	}
	if len(l2.History) != 2 {
		t.Fatalf("history not appended: %+v", l2.History)
	}
}

func TestApplyFreshnessConfirmationRecoversFromMalformedLedger(t *testing.T) {
	// A corrupt/hand-edited ledger must not drop the confirmation — start fresh.
	js, err := applyFreshnessConfirmation("{not json", "knowledgebase", "iteration-0", "step-x", freshnessActionReviewed, "2026-07-18T12:00:00Z")
	if err != nil {
		t.Fatalf("apply on malformed: %v", err)
	}
	l := parseLedger(t, js)
	if l.Store != "knowledgebase" || l.ConfirmCount != 1 || len(l.History) != 1 {
		t.Fatalf("did not recover to fresh ledger: %+v", l)
	}
}

func TestApplyFreshnessConfirmationEmptyActionDefaultsToReviewed(t *testing.T) {
	js, err := applyFreshnessConfirmation("", "knowledgebase", "iteration-0", "step-x", "", "2026-07-18T12:00:00Z")
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if l := parseLedger(t, js); l.History[0].Action != freshnessActionReviewed {
		t.Fatalf("empty action should default to reviewed: %+v", l.History)
	}
}

func TestApplyFreshnessConfirmationBoundsHistory(t *testing.T) {
	js := ""
	var err error
	total := freshnessHistoryCap + 10
	for i := 0; i < total; i++ {
		js, err = applyFreshnessConfirmation(js, "learnings", "iteration-0", "step-x", freshnessActionReviewed, "2026-07-18T12:00:00Z")
		if err != nil {
			t.Fatalf("apply %d: %v", i, err)
		}
	}
	l := parseLedger(t, js)
	if l.ConfirmCount != total {
		t.Fatalf("confirm_count should be full total %d, got %d", total, l.ConfirmCount)
	}
	if len(l.History) != freshnessHistoryCap {
		t.Fatalf("history should be capped at %d, got %d", freshnessHistoryCap, len(l.History))
	}
}

func TestFreshnessLedgerPaths(t *testing.T) {
	if got := knowledgebaseFreshnessLedgerPath(); !strings.HasSuffix(got, "/_freshness.json") || !strings.HasPrefix(got, "knowledgebase/") {
		t.Fatalf("kb ledger path wrong: %s", got)
	}
}

func TestApplyItemFreshnessStampsChangedAndKeepsUnchanged(t *testing.T) {
	// Round 1: two new items on an empty ledger — both stamped updated @ run A.
	items := applyItemFreshness(nil, map[string]string{
		"references/selectors.md": "h1",
		"references/auth-flow.md": "h1",
	}, "iteration-0/A", "step-x", "2026-07-18T10:00:00Z")
	if len(items) != 2 || items["references/selectors.md"].ConfirmCount != 1 || items["references/selectors.md"].LastConfirmedRun != "iteration-0/A" {
		t.Fatalf("round1 wrong: %+v", items)
	}

	// Round 2 @ run B: selectors changed (h2), auth unchanged (h1).
	items = applyItemFreshness(items, map[string]string{
		"references/selectors.md": "h2",
		"references/auth-flow.md": "h1",
	}, "iteration-0/B", "step-x", "2026-07-18T11:00:00Z")

	sel := items["references/selectors.md"]
	if sel.ConfirmCount != 2 || sel.LastConfirmedRun != "iteration-0/B" || sel.Hash != "h2" {
		t.Fatalf("changed item not re-stamped: %+v", sel)
	}
	auth := items["references/auth-flow.md"]
	if auth.ConfirmCount != 1 || auth.LastConfirmedRun != "iteration-0/A" {
		t.Fatalf("unchanged item must keep its older stamp (this is the aging signal): %+v", auth)
	}
}

func TestApplyItemFreshnessDropsRemovedItems(t *testing.T) {
	items := applyItemFreshness(nil, map[string]string{"a.md": "h1", "b.md": "h1"}, "r1", "s", "t1")
	// b.md gone from disk this run.
	items = applyItemFreshness(items, map[string]string{"a.md": "h1"}, "r2", "s", "t2")
	if _, ok := items["b.md"]; ok {
		t.Fatalf("removed item should be dropped: %+v", items)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %+v", items)
	}
}

func TestApplyItemFreshnessPreservesWhenSnapshotEmpty(t *testing.T) {
	items := map[string]ItemFreshness{"a.md": {Hash: "h1", ConfirmCount: 3, LastConfirmedRun: "r1"}}
	// Empty snapshot (dir missing/unreadable) must NOT wipe prior records.
	got := applyItemFreshness(items, map[string]string{}, "r2", "s", "t2")
	if len(got) != 1 || got["a.md"].ConfirmCount != 3 {
		t.Fatalf("empty snapshot should preserve items: %+v", got)
	}
}

func TestSnapshotItemHashesReadsMarkdownOnly(t *testing.T) {
	dir := t.TempDir()
	mustWrite := func(rel, content string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite("SKILL.md", "index")
	mustWrite("references/selectors.md", "sel")
	mustWrite("_freshness.json", "{}")           // infra, not .md → excluded
	mustWrite(".learning_metadata.json", "{}")   // dotfile → excluded
	mustWrite("references/notes.txt", "ignored") // non-md → excluded

	snap, err := snapshotItemHashes(dir)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if len(snap) != 2 {
		t.Fatalf("expected only 2 .md files, got %+v", snap)
	}
	if _, ok := snap["SKILL.md"]; !ok {
		t.Fatalf("SKILL.md missing: %+v", snap)
	}
	if _, ok := snap["references/selectors.md"]; !ok {
		t.Fatalf("references/selectors.md missing: %+v", snap)
	}
	// Same content → stable hash; different content → different hash.
	if snap["SKILL.md"] == snap["references/selectors.md"] {
		t.Fatalf("different content should hash differently")
	}
}

func TestSnapshotItemHashesMissingDirIsEmpty(t *testing.T) {
	snap, err := snapshotItemHashes(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Fatalf("missing dir should not error: %v", err)
	}
	if len(snap) != 0 {
		t.Fatalf("missing dir should be empty: %+v", snap)
	}
}
