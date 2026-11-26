package mcpclient

import (
	"github.com/manishiitg/mcpagent/oauth"
	"testing"
)

func TestMergeMCPConfigsPreservesDistinctAccounts(t *testing.T) {
	base := &MCPConfig{MCPServers: map[string]MCPServerConfig{"Linear": {OAuth: &oauth.OAuthConfig{TokenFile: "base.json"}}}}
	overlay := &MCPConfig{MCPServers: map[string]MCPServerConfig{"Linear": {OAuth: &oauth.OAuthConfig{TokenFile: "other.json"}}, "Linear-base": {Description: "existing"}}}
	merged := MergeMCPConfigs(base, overlay)
	if merged.MCPServers["Linear"].OAuth.TokenFile != "other.json" || merged.MCPServers["Linear-base-2"].OAuth.TokenFile != "base.json" || merged.MCPServers["Linear-base"].Description != "existing" {
		t.Fatal("lost or replaced an account")
	}
	overlay.MCPServers["Linear"] = base.MCPServers["Linear"]
	if len(MergeMCPConfigs(base, overlay).MCPServers) != 2 {
		t.Fatal("same credentials duplicated")
	}
	if len(base.MCPServers) != 1 {
		t.Fatal("mutated source")
	}
}
