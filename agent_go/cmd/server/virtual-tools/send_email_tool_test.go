package virtualtools

import (
	"context"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
)

// send_email exists because summary delivery uses saved recipients (PLAT-736): it must refuse a call that names none,
// and it must be held whole when the Outward permission is ask.
func TestSendEmailRequiresRecipientsAndRespectsOutwardHold(t *testing.T) {
	valid := map[string]interface{}{"to": []interface{}{"person@example.com"}, "subject": "Report", "body": "ready"}

	if _, err := handleSendEmail(context.Background(), map[string]interface{}{"subject": "Report", "body": "ready"}); err == nil || !strings.Contains(err.Error(), "at least one recipient") {
		t.Fatalf("a call without recipients must be refused and name the missing recipient, got %v", err)
	}
	if _, err := handleSendEmail(common.WithOutwardHeld(context.Background()), valid); err == nil || !strings.Contains(err.Error(), "Outward") {
		t.Fatalf("send_email must be refused whole while Outward is ask, got %v", err)
	}
}
