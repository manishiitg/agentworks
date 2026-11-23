package mcpagent

import (
	"reflect"
	"testing"

	"github.com/manishiitg/mcpagent/mcpclient"
)

func TestCanonicalizeRequestedServersDeduplicatesAliasesBeforeConnect(t *testing.T) {
	config := &mcpclient.MCPConfig{MCPServers: map[string]mcpclient.MCPServerConfig{
		"google-sheets": {Command: "uvx"},
	}}

	got := canonicalizeRequestedServers(config, []string{"google_sheets", "google-sheets"})
	want := []string{"google-sheets"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("canonical servers = %#v, want %#v", got, want)
	}
}

func TestCanonicalizeRequestedServersPreservesUnknownNamesOnce(t *testing.T) {
	config := &mcpclient.MCPConfig{MCPServers: map[string]mcpclient.MCPServerConfig{}}

	got := canonicalizeRequestedServers(config, []string{"missing", "missing"})
	want := []string{"missing"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("canonical servers = %#v, want %#v", got, want)
	}
}

func TestRuntimeOverrideForServerAcceptsSanitizedAlias(t *testing.T) {
	overrides := mcpclient.RuntimeOverrides{
		"google_sheets": {ArgsAppend: []string{"--readonly"}},
	}

	override, ok := runtimeOverrideForServer(overrides, "google-sheets")
	if !ok {
		t.Fatal("runtime override keyed by alias was not resolved")
	}
	if !reflect.DeepEqual(override.ArgsAppend, []string{"--readonly"}) {
		t.Fatalf("ArgsAppend = %#v, want readonly flag", override.ArgsAppend)
	}
}
