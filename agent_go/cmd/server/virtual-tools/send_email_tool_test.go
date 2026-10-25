package virtualtools

import (
	"context"
	"strings"
	"testing"
)

// PLAT-736: a direct email names its recipients; it never takes summary recipients.
func TestSendEmailRequiresRecipients(t *testing.T) {
	if _, err := handleSendEmail(context.Background(), map[string]interface{}{"subject": "Report", "body": "ready"}); err == nil || !strings.Contains(err.Error(), "at least one recipient") {
		t.Fatalf("email without recipients must name the missing recipient, got %v", err)
	}
}
