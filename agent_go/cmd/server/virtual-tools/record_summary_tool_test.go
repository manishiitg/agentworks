package virtualtools

import (
	"context"
	"strings"
	"testing"
)

// record_summary supplies content only (PLAT-736): recipients, channels and webhooks come from the workflow's saved
// settings, so a call that tries to name them, or that is not a run or Pulse summary, is refused.
func TestRecordSummaryRefusesRecipientsChannelsAndOtherKinds(t *testing.T) {
	base := func() map[string]interface{} {
		return map[string]interface{}{"kind": "run_summary", "message": "3/3 checks passed"}
	}
	for _, refused := range []string{"email_to", "email_cc", "exclude_channels", "delivery_mode", "notification_kind", "message_for_user"} {
		args := base()
		args[refused] = []interface{}{"someone@example.com"}
		if _, err := handleRecordSummary(context.Background(), args); err == nil || !strings.Contains(err.Error(), refused) {
			t.Fatalf("%s must be refused by record_summary, got %v", refused, err)
		}
	}
	args := base()
	args["kind"] = "general"
	if _, err := handleRecordSummary(context.Background(), args); err == nil || !strings.Contains(err.Error(), "run_summary or pulse_summary") {
		t.Fatalf("a non-summary kind must be refused, got %v", err)
	}
}
