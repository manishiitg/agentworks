//go:build linux

package security

import (
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// PLAT-872-86: the provider keeps its own copy of this list for processes it starts as a slot (the Claude SDK bridge
// held GOG_KEYRING_PASSWORD and SUPABASE_* because its copy was shorter). A secret added on one side fails here until
// it is on both.
func TestPlatformSecretEnvListMatchesProviderSlotScrub(t *testing.T) {
	providerDrops := func(name string) bool {
		return llmtypes.IsPlatformSecretEnvName(name) || llmtypes.IsServerOwnedSecretKey(name)
	}
	for _, name := range platformSecretEnvNames {
		if !providerDrops(name) {
			t.Errorf("provider slot scrub keeps %s", name)
		}
	}
	for _, prefix := range platformSecretEnvPrefixes {
		if !providerDrops(prefix + "X") {
			t.Errorf("provider slot scrub keeps %sX", prefix)
		}
	}
	for _, name := range append(append([]string{}, llmtypes.PlatformSecretEnvNames...), prefixed(llmtypes.PlatformSecretEnvPrefixes)...) {
		if len(ScrubPlatformSecretEnv([]string{name + "=x"})) != 0 {
			t.Errorf("launcher scrub keeps %s", name)
		}
	}
}

func prefixed(prefixes []string) []string {
	out := make([]string, 0, len(prefixes))
	for _, prefix := range prefixes {
		out = append(out, prefix+"X")
	}
	return out
}
