package services

import "testing"

// PLAT-746: a successful CLI exit with no message ID must not confirm a send.
func TestGoogleSendReceiptRequiresMessageID(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		{"id", `{"id":"message-1","threadId":"thread-1"}`, "message-1"},
		{"messageId", `{"messageId":"message-2"}`, "message-2"},
		{"message_id", `{"message_id":"message-3"}`, "message-3"},
		{"thread only", `{"threadId":"thread-1","thread_id":"thread-2"}`, ""},
		{"empty", "", ""},
		{"garbage", "send completed", ""},
		{"missing id", `{}`, ""},
		{"invalid id type", `{"id":42,"threadId":"thread-1"}`, ""},
		{"blank id", `{"id":"   ","threadId":"thread-1"}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := parseGwsMessageID([]byte(tc.body))
			if got != tc.want {
				t.Fatalf("message ID = %q, want %q; a thread ID or missing response cannot confirm a send", got, tc.want)
			}
		})
	}
}
