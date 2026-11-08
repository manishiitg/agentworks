package server

import (
	"context"
	"strings"
	"testing"
)

func TestSlackCLIReadValidation(t *testing.T) {
	for _, tc := range []struct {
		method string
		params map[string]interface{}
	}{
		{"chat.delete", nil}, {"conversations.history", map[string]interface{}{"token": "secret"}},
		{"conversations.history", map[string]interface{}{"channel": "COTHER"}},
		{"conversations.history", map[string]interface{}{"limit": 1000.}},
		{"conversations.replies", nil},
	} {
		if _, err := validateSlackCLIRead(tc.method, "C123", tc.params); err == nil {
			t.Fatalf("accepted unsafe call: %s %v", tc.method, tc.params)
		}
	}
	args, err := validateSlackCLIRead("conversations.replies", "C123", map[string]interface{}{"ts": "1789711468.455929", "limit": 20.})
	if err != nil || args["channel"] != "C123" || args["limit"] != 20 {
		t.Fatalf("valid read failed: %v %v", args, err)
	}
}

// A trusted full-mode turn gets the open Slack tool (any method, route_id
// optional); a read-only turn keeps the channel-scoped read-and-reply tool.
func TestSlackToolFullAccessOnlyForTrustedTurns(t *testing.T) {
	api := &StreamingAPI{}
	full := &recordingRegistrar{}
	if err := api.registerSlackBotTools(full, "session", "Workflow/alpha", "", false, true); err != nil {
		t.Fatal(err)
	}
	if desc := full.tools["slack"].desc; !strings.Contains(desc, "any Slack Web API method") {
		t.Fatalf("trusted turn got the limited Slack tool: %q", desc)
	}
	limited := &recordingRegistrar{}
	if err := api.registerSlackBotTools(limited, "session", "Workflow/alpha", "", false, false); err != nil {
		t.Fatal(err)
	}
	if desc := limited.tools["slack"].desc; strings.Contains(desc, "any Slack Web API method") {
		t.Fatalf("read-only turn got the open Slack tool: %q", desc)
	}

	// The open tool never takes credentials or anything but a method name.
	for _, args := range []map[string]interface{}{
		{"method": "views.publish", "parameters": map[string]interface{}{"token": "xoxb-other", "user_id": "U1"}},
		{"method": "--config-dir /etc"},
		{"method": "views.publish", "parameters": "not an object"},
	} {
		if _, err := full.tools["slack"].exec(context.Background(), args); err == nil {
			t.Fatalf("open Slack tool accepted %v", args)
		}
	}
}
