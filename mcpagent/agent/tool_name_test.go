package mcpagent

import "testing"

// PLAT-519: aliases are short and never the internal key; two connections of
// one server get stable distinct aliases.
func TestMCPToolAliasesAndVisibleNames(t *testing.T) {
	aliases := mcpToolAliases([]string{
		"ue03d5aec7071d9d6e888aca57a337a56__neon_46fb2cad6010",
		"ue03d5aec7071d9d6e888aca57a337a56__neon_9c1d00aa7713",
		"google-sheets",
	}, nil)
	for server, want := range map[string]string{
		"ue03d5aec7071d9d6e888aca57a337a56__neon_46fb2cad6010": "neon_46fb",
		"ue03d5aec7071d9d6e888aca57a337a56__neon_9c1d00aa7713": "neon_9c1d",
		"google-sheets": "google-sheets",
	} {
		if aliases[server] != want {
			t.Errorf("alias(%s) = %q, want %q", server, aliases[server], want)
		}
	}

	// A server name that changes with its credentials must not reach the alias:
	// the host's declared alias stands, and a plain server sharing it moves aside.
	scoped := "vault_c-94acea49e7a07fde__scope_86accc0011223344"
	aliases = mcpToolAliases([]string{scoped, "notion"}, map[string]string{scoped: "notion"})
	if aliases[scoped] != "notion" || aliases["notion"] == "notion" {
		t.Errorf("declared alias lost: %v", aliases)
	}
}
