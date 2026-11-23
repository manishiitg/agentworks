package mcpclient

import "testing"

func TestResolveServerReturnsConfiguredNameForAlias(t *testing.T) {
	config := &MCPConfig{MCPServers: map[string]MCPServerConfig{
		"google-sheets": {Command: "uvx"},
	}}

	name, server, err := config.ResolveServer("google_sheets")
	if err != nil {
		t.Fatalf("ResolveServer returned an error: %v", err)
	}
	if name != "google-sheets" {
		t.Fatalf("resolved name = %q, want %q", name, "google-sheets")
	}
	if server.Command != "uvx" {
		t.Fatalf("resolved command = %q, want %q", server.Command, "uvx")
	}
}

func TestResolveServerPrefersExactConfiguredName(t *testing.T) {
	config := &MCPConfig{MCPServers: map[string]MCPServerConfig{
		"google-sheets": {Command: "hyphen"},
		"google_sheets": {Command: "underscore"},
	}}

	name, server, err := config.ResolveServer("google_sheets")
	if err != nil {
		t.Fatalf("ResolveServer returned an error: %v", err)
	}
	if name != "google_sheets" || server.Command != "underscore" {
		t.Fatalf("exact lookup resolved to name=%q command=%q", name, server.Command)
	}
}
