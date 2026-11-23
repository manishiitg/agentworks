// Package launchscrub builds the credential scrub shared by the coding-agent
// tmux launchers (Claude Code, Codex CLI, Cursor CLI).
package launchscrub

import (
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/internal/shelllaunch"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Scoped builds the dynamic credential scrub for a tmux launch.
//
// The unset list computed from os.Environ() cannot be complete: the pane
// inherits the long-lived tmux SERVER's environment, so a credential that
// drifted there (a backend restart, an older session) is invisible to this
// process and would survive. The scrub matches by pattern against whatever
// actually exists at exec time instead.
//
// Keep is everything legitimately in play: the caller's declared scope, and
// the credentials this adapter derived itself (its provider key, the MCP
// routes built from the caller's config). Scrubbing those would strip the
// credentials the launch just assembled.
func Scoped(adapterEnv, declaredEnv []string, opts *llmtypes.CallOptions) *shelllaunch.ScopeScrub {
	if !llmtypes.CodingAgentScopeDeclared(opts) {
		return nil
	}
	keep := make([]string, 0, len(adapterEnv)+len(declaredEnv))
	for _, entries := range [][]string{adapterEnv, declaredEnv} {
		for _, entry := range entries {
			if key, _, found := strings.Cut(entry, "="); found {
				keep = append(keep, key)
			}
		}
	}
	return &shelllaunch.ScopeScrub{
		Prefixes: llmtypes.ScopedCredentialPrefixes(),
		Names:    llmtypes.ScopedCredentialNames(),
		Keep:     keep,
	}
}
