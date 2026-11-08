package services

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// PLAT-746: a successful CLI exit with no message ID must not confirm a send.
// Only the external Google CLI is replaced; all three production send paths run.
func TestGoogleSendReceiptRequiresMessageID(t *testing.T) {
	for _, transport := range []string{"gws", "gog plain", "gog rich"} {
		t.Run(transport, func(t *testing.T) {
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
					dir := t.TempDir()
					binary, response, calls := filepath.Join(dir, "google-cli"), filepath.Join(dir, "response.json"), filepath.Join(dir, "calls")
					if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf x >> \"$MAIL_RECEIPT_CALLS\"\ncat \"$MAIL_RECEIPT_RESPONSE\"\n"), 0700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(response, []byte(tc.body), 0600); err != nil {
						t.Fatal(err)
					}
					t.Setenv("MAIL_RECEIPT_RESPONSE", response)
					t.Setenv("MAIL_RECEIPT_CALLS", calls)
					g := &GmailService{gwsPath: binary, gogPath: binary, useGog: transport != "gws"}
					cfg := &GmailConfig{Token: "test-token", gogHome: dir}
					var got string
					var err error
					if transport == "gog plain" {
						got, err = g.sendGog(context.Background(), binary, cfg, "recipient@example.com", "Report", "Evidence")
					} else {
						got, err = g.sendRaw(context.Background(), cfg, "recipient@example.com", nil, "Report", "Evidence", "", nil)
					}
					if got != tc.want {
						t.Fatalf("message ID = %q, want %q; a thread ID or missing response cannot confirm a send", got, tc.want)
					}
					if tc.want == "" && !errors.Is(err, errGmailSendUnconfirmed) {
						t.Fatalf("missing message ID must report an unknown outcome, got %v", err)
					}
					if tc.want != "" && err != nil {
						t.Fatalf("actual message ID refused: %v", err)
					}
					attempts, readErr := os.ReadFile(calls)
					if readErr != nil || string(attempts) != "x" {
						t.Fatalf("send must execute exactly once, got %q: %v", attempts, readErr)
					}
				})
			}
		})
	}
}
