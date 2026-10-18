package pulsemodules

import "testing"

func TestAcceptedForReviewReceiptsCoversCurrentModules(t *testing.T) {
	accepted := map[string]bool{}
	for _, id := range AcceptedForReviewReceipts() {
		accepted[id] = true
	}
	for _, m := range All {
		if !accepted[m.ID] {
			t.Fatalf("current module %q is not accepted for reviewer artifacts — its results would silently fail to persist", m.ID)
		}
	}
}
