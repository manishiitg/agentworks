package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// PLAT-746-941: a network that accepts only delivered receipts caused the
// failed head row to reconnect the entire account and starve later mail.
func TestAppInboxContinuesWhenFailedReceiptIsUnsupported(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusServiceUnavailable} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var failures atomic.Int32
			var accepted atomic.Bool
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var args struct {
					Action   string `json:"action"`
					After    int    `json:"after"`
					Delivery string `json:"delivery"`
				}
				if json.NewDecoder(r.Body).Decode(&args) != nil {
					t.Error("invalid request")
					w.WriteHeader(400)
					return
				}
				switch args.Action {
				case "inbox":
					messages := []desktopAppIncoming{}
					if args.After < 1 {
						messages = append(messages, desktopAppIncoming{ID: "failed-message", Sequence: 1})
					}
					if args.After < 2 && !accepted.Load() {
						messages = append(messages, desktopAppIncoming{ID: "later-message", Sequence: 2})
					}
					next := args.After
					if len(messages) > 0 {
						next = messages[len(messages)-1].Sequence
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"messages": messages, "next_cursor": next})
				case "ack":
					if args.Delivery == "failed" {
						failures.Add(1)
						externalError(w, status, "invalid_arguments", "Use delivery=delivered when this chat has received the message.")
						return
					}
					accepted.Store(true)
					_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
				default:
					w.WriteHeader(400)
				}
			}))
			defer remote.Close()
			api := &StreamingAPI{}
			connection := desktopConnection{ID: "login", Server: remote.URL}
			recipient := desktopAppRecipient{Record: ProductConversationRecord{ConversationID: "physical-chat"}, Registration: desktopAppAgentRegistration{Name: "recipient", Attachment: "current-attachment"}}
			refused, delivered := 0, 0
			deliver := func(_ context.Context, _, _ string, _ ProductConversationRecord, m desktopAppIncoming) error {
				if m.ID == "failed-message" {
					refused++
					return &desktopAppAdmissionError{status: 403, reason: "submission_outcome_replay"}
				}
				delivered++
				return nil
			}
			err := api.receiveDesktopAppInboxWithDelivery(context.Background(), "owner", connection, recipient, deliver)
			key := desktopFailedAckKey{"owner", "login", "physical-chat", "failed-message"}
			if status != http.StatusBadRequest {
				if err == nil || delivered != 0 || api.desktopFailedAcks.contains(key) {
					t.Fatal("authorization or transient receipt failure was swallowed")
				}
				return
			}
			if err != nil || refused != 1 || delivered != 1 || !accepted.Load() {
				t.Fatal("failed row blocked later delivery")
			}
			// A reconnect/attachment renewal must not retry the failed head row.
			recipient.Registration.Attachment = "renewed-attachment"
			if err = api.receiveDesktopAppInboxWithDelivery(context.Background(), "owner", connection, recipient, deliver); err != nil {
				t.Fatal(err)
			}
			if refused != 1 || delivered != 1 || failures.Load() != 1 {
				t.Fatal("permanent refusal replayed on reconnect")
			}
			if api.desktopFailedAcks.contains(desktopFailedAckKey{"other", "login", "physical-chat", "failed-message"}) || api.desktopFailedAcks.contains(desktopFailedAckKey{"owner", "other", "physical-chat", "failed-message"}) || api.desktopFailedAcks.contains(desktopFailedAckKey{"owner", "login", "replacement-chat", "failed-message"}) {
				t.Fatal("failed-ack skip crossed recipient/account scope")
			}
			for i := 0; i < desktopFailedAckLimit; i++ {
				api.desktopFailedAcks.remember(desktopFailedAckKey{"owner", "login", "physical-chat", fmt.Sprint(i)})
			}
			if len(api.desktopFailedAcks.order) != desktopFailedAckLimit || len(api.desktopFailedAcks.keys) != desktopFailedAckLimit || api.desktopFailedAcks.contains(key) {
				t.Fatal("failed-ack cache is unbounded or failed to evict oldest")
			}
		})
	}
}
