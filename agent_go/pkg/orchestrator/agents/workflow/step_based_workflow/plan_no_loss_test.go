package step_based_workflow

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCheckPlanNoLossDroppedThresholdFails pins the consolidator's one hard
// rule: a rewrite that loses a threshold is not a pure text move.
func TestCheckPlanNoLossDroppedThresholdFails(t *testing.T) {
	old := "Admit only semantic_fit=high and fit>=12. Hire<20% with posts>=5 rejects. Read VAR_PINNED_JOB_ID; use code/shared/bid_preparation_gate.py. Measured on 2026-09-05: 14 claims failed."
	guide := "# Rules\n- `semantic_fit=high` and fit >= 12.\n- Hire rate < 20% with >= 5 posts rejects.\n"
	desc := "## Goal\nPick one job.\n## Inputs\n- `VAR_PINNED_JOB_ID`\n## Guides\n- `knowledgebase/notes/rules.md`\n## Done when\n`code/shared/bid_preparation_gate.py` wrote the gate."

	pass := CheckPlanNoLoss(old, desc, "", map[string]string{"knowledgebase/notes/rules.md": guide}, []string{"14"})
	if !pass.Pass {
		t.Fatalf("faithful move must pass, missing=%+v rejected=%v", pass.Missing, pass.RejectedDrops)
	}
	dropped := strings.Replace(guide, "fit >= 12", "a good fit", 1)
	fail := CheckPlanNoLoss(old, desc, "", map[string]string{"knowledgebase/notes/rules.md": dropped}, []string{"14"})
	if fail.Pass || !noLossMissingHas(fail, ">=12") {
		t.Fatalf("dropped threshold must fail and be named, got pass=%v missing=%+v", fail.Pass, fail.Missing)
	}
	// History may be dropped only when it is history: "12" is a rule.
	if r := CheckPlanNoLoss(old, desc, "", map[string]string{"knowledgebase/notes/rules.md": dropped}, []string{">=12", "fit>=12"}); r.Pass {
		t.Fatal("a rule token cannot be acknowledged as dropped history")
	}
}

func noLossMissingHas(r NoLossReport, needle string) bool {
	for _, m := range r.Missing {
		if strings.Contains(strings.ReplaceAll(m.Token, " ", ""), needle) {
			return true
		}
	}
	return false
}

// TestCheckPlanNoLossUpworkPilot runs the checker on the real Upwork
// bid-pick-job step (owner pilot, 2026-10-06): the reviewed rewrite must
// pass, and the same rewrite with one floor removed must fail. The data is
// the owner's workflow content and stays outside the repository; set
// PLAT556_PILOT_DIR to the folder holding bid_pick_job_original.json,
// bid-pick-job-pilot/ and upwork-backup-*/ to run it.
func TestCheckPlanNoLossUpworkPilot(t *testing.T) {
	dir := os.Getenv("PLAT556_PILOT_DIR")
	if dir == "" {
		t.Skip("PLAT556_PILOT_DIR not set")
	}
	read := func(rel string) string {
		b, err := os.ReadFile(filepath.Join(dir, rel))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	var original struct {
		Description string        `json:"description"`
		Items       []interface{} `json:"items"`
	}
	if err := json.Unmarshal([]byte(read("bid_pick_job_original.json")), &original); err != nil {
		t.Fatal(err)
	}
	oldText := original.Description + "\n" + noLossItemsText(original.Items)
	desc := read("bid-pick-job-pilot/1-description.md")
	items := read("bid-pick-job-pilot/2-items.md")
	backups, _ := filepath.Glob(filepath.Join(dir, "upwork-backup-*"))
	if len(backups) == 0 {
		t.Fatal("no upwork-backup-* folder")
	}
	files, _ := NoLossNamedFiles(desc)
	guides := map[string]string{}
	for _, f := range files {
		switch filepath.Base(f) {
		case "bid-admission-and-claim-rules.md":
			guides[f] = read("bid-pick-job-pilot/3-kb-bid-admission-and-claim-rules.md")
		case "bid-claim-protocol.md":
			guides[f] = read("bid-pick-job-pilot/4-skill-bid-claim-protocol.md")
		default:
			if b, err := os.ReadFile(filepath.Join(backups[0], f)); err == nil {
				guides[f] = string(b)
			}
		}
	}
	report := CheckPlanNoLoss(oldText, desc, items, guides, nil)
	t.Logf("tokens=%d resolved=%d missing=%d acknowledged=%d", report.TokensChecked, len(guides), len(report.Missing), len(report.AcknowledgedDrops))
	for _, m := range report.Missing {
		t.Logf("missing %s %q in %q", m.Kind, m.Token, m.Context)
	}
	if !report.Pass {
		t.Fatalf("reviewed pilot rewrite must pass")
	}
	kb := "knowledgebase/notes/bid-admission-and-claim-rules.md"
	guides[kb] = strings.Replace(guides[kb], "Fixed budget < $300 rejects.", "Very small fixed budgets reject.", 1)
	broken := CheckPlanNoLoss(oldText, desc, items, guides, nil)
	if broken.Pass || !noLossMissingHas(broken, "300") {
		t.Fatalf("pilot rewrite without the $300 floor must fail naming it, got pass=%v missing=%+v", broken.Pass, broken.Missing)
	}
	for name, text := range guides {
		guides[name] = strings.ReplaceAll(text, "hard_floors_cleared", "floors")
	}
	if r := CheckPlanNoLoss(oldText, desc, items, guides, nil); r.Pass || !noLossMissingHas(r, "hard_floors_cleared") {
		t.Fatalf("pilot rewrite without hard_floors_cleared must fail naming it, got %+v", r.Missing)
	}
}
