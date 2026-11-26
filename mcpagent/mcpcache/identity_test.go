package mcpcache

import (
	"github.com/manishiitg/mcpagent/mcpclient"
	"github.com/manishiitg/mcpagent/oauth"
	"testing"
)

func TestOAuthMetadataCacheIsAccountScoped(t *testing.T) {
	a := mcpclient.MCPServerConfig{URL: "https://example.invalid/mcp", OAuth: &oauth.OAuthConfig{TokenFile: "/tokens/alice.json"}} // #nosec G101 -- synthetic path, not a credential.
	b := a
	b.OAuth = &oauth.OAuthConfig{TokenFile: "/tokens/bob.json"} // #nosec G101 -- synthetic path, not a credential.
	if GenerateUnifiedCacheKey("example", a) == GenerateUnifiedCacheKey("example", b) {
		t.Fatal("different accounts must not share tool metadata")
	}
	if GenerateUnifiedCacheKey("example", a) != GenerateUnifiedCacheKey("example", a) {
		t.Fatal("cache key must be stable")
	}
}
