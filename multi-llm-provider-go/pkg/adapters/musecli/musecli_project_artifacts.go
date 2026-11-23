package musecli

import (
	"github.com/manishiitg/multi-llm-provider-go/pkg/projectfile"
	"path/filepath"
	"strings"
)

// Muse 1.4.1 truncates project rules after 32,000 UTF-8 bytes. Count the
// complete projected file, including operator rules, before accepting it as
// the sole instruction carrier. Oversized projections use the inline fallback.
const museProjectRulesMaxBytes = 32000

// Project-instruction projection for muse, mirroring codex's
// writeCodexProjectAgentsFile: the per-session system prompt is written to
// <workingDir>/AGENTS.md, which muse auto-loads as project instructions in
// a trusted workspace. Muse has no --system-prompt flag, so in
// project-instruction-only mode this file is the SOLE carrier — the adapter
// skips typing the preamble inline and the turn stays small.
//
// Byte-restore semantics match codex: with restorePrior the cleanup puts an
// operator's pre-existing AGENTS.md back byte-for-byte, otherwise the
// projected file is deleted. Either way a failed write returns an error so
// the caller can fall back to inline typing and the prompt is never lost.
func writeMuseProjectAgentsFile(workingDir, systemPrompt string, restorePrior bool) (func(), error) {
	workingDir = strings.TrimSpace(workingDir)
	if workingDir == "" {
		return func() {}, nil
	}
	// The prompt is added to AGENTS.md as a marked block, counted per
	// session: a project's own AGENTS.md and other sessions sharing the folder
	// are never overwritten or deleted. restorePrior is obsolete.
	_ = restorePrior
	token, err := projectfile.AcquireLeaseLimit(filepath.Join(workingDir, "AGENTS.md"), systemPrompt, museProjectRulesMaxBytes)
	if err != nil {
		return nil, err
	}
	return func() { projectfile.ReleaseToken(token) }, nil
}

// museInlinePrompt is the legacy concatenation: system texts joined ahead
// of the human turn. Used by the exec lane and as the file-only fallback
// when AGENTS.md projection is disabled or fails.
func museInlinePrompt(system []string, human string) string {
	if len(system) > 0 {
		return strings.Join(system, "\n\n") + "\n\n" + human
	}
	return human
}

// projectMuseAgentsForTurn projects the system prompt for bounded
// (non-pooled) turns, which boot and tear down one TUI per turn. Returns
// the teardown restore and whether the file carries this turn; on any
// failure both are nil/false and the caller types inline instead.
func projectMuseAgentsForTurn(workdir string, system []string, wantAgents, restorePrior bool) (func(), bool) {
	if !wantAgents || len(system) == 0 || strings.TrimSpace(workdir) == "" {
		return nil, false
	}
	restore, err := writeMuseProjectAgentsFile(workdir, strings.Join(system, "\n\n"), restorePrior)
	if err != nil {
		return nil, false
	}
	return restore, true
}
