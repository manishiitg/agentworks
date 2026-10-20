package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
)

// Drive real Streamable HTTP discovery, topic reads, package reads and cache
// hits for read-only, Crew-only and Relay-authoring connections.
func TestExternalMCPGuidanceRefresh(t *testing.T) {
	f := newExternalToolsFixture(t)
	for _, tc := range []struct {
		name        string
		scopes      []string
		crew, relay bool
	}{
		{"read", []string{"workflows:read", "files:read"}, false, false},
		{"crew-read", []string{"crews:read"}, false, false},
		{"crew-author", []string{"crews:read", "crews:write"}, true, false},
		{"relay-author", []string{"workflows:read", "files:read", "runs:execute", "relays:write"}, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			claims := &UserClaims{UserID: "owner", Username: "owner", AccessToken: &accesstokens.Token{ID: tc.name, Scopes: tc.scopes, AllWorkflows: true, AllCrews: true}}
			srv := serveExternalMCP(t, f.api, claims)
			ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
			defer cancel()
			cli := dialExternalMCP(t, ctx, srv.URL+externalMCPPath)
			initializeExternalMCP(t, ctx, cli)
			call := func(action string, args map[string]any) map[string]any {
				t.Helper()
				if args == nil {
					args = map[string]any{}
				}
				args["action"] = action
				result := callRemoteTool(t, ctx, cli, externalMCPToolCall, map[string]any{"name": "help", "arguments": args})
				requireRemoteSuccess(t, result, action)
				var body map[string]any
				if err := json.Unmarshal([]byte(marshalStructured(t, result)), &body); err != nil {
					t.Fatal(err)
				}
				return body
			}
			list := call("topics", nil)
			topics := map[string]string{}
			for _, raw := range list["topics"].([]any) {
				item := raw.(map[string]any)
				topics[item["name"].(string)] = item["version"].(string)
			}
			if (topics["crew-authoring"] != "") != tc.crew || (topics["relay-builder"] != "") != tc.relay || topics["plan-editing-tools"] != "" {
				t.Fatalf("scope/private boundary: %v", topics)
			}
			for name, version := range topics {
				body := call("topic", map[string]any{"topic": name})
				content := body["content"].(string)
				if body["version"] != version || content == "" || strings.Contains(content, "external_scopes:") || strings.HasPrefix(content, "PATH DISCIPLINE") {
					t.Fatalf("render/version boundary for %s: %v", name, body)
				}
				cached := call("topic", map[string]any{"topic": name, "known_version": version})
				if cached["unchanged"] != true || cached["content"] != nil {
					t.Fatalf("cache miss for %s", name)
				}
			}
			pack := call("skills", map[string]any{"topics": []any{"agent"}})
			for _, raw := range pack["skills"].([]any) {
				item := raw.(map[string]any)
				file := item["files"].([]any)[0].(map[string]any)
				if item["name"] == "agent" {
					if !strings.Contains(file["content"].(string), "name: agent") || !strings.Contains(file["content"].(string), "Reference material") {
						t.Fatal("missing installable reference/note")
					}
				} else if file["content"] != nil {
					t.Fatal("unrequested topic body returned")
				}
			}
			cached := call("skills", map[string]any{"topics": []any{"agent"}, "known_versions": map[string]any{"agent": topics["agent"]}})
			for _, raw := range cached["skills"].([]any) {
				item := raw.(map[string]any)
				if item["name"] == "agent" && (item["unchanged"] != true || item["files"].([]any)[0].(map[string]any)["content"] != nil) {
					t.Fatal("package cache miss")
				}
			}
			denied := callRemoteTool(t, ctx, cli, externalMCPToolCall, map[string]any{"name": "help", "arguments": map[string]any{"action": "skills", "topics": []any{"plan-editing-tools"}}})
			if !denied.IsError {
				t.Fatal("unmarked source was exposed")
			}
			removed := callRemoteTool(t, ctx, cli, externalMCPToolCall, map[string]any{"name": "help", "arguments": map[string]any{"action": "topic", "topic": "message-sequence"}})
			requireRemoteError(t, removed, "removed topic", "unknown_topic")
		})
	}
}
