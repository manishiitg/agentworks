package llmproviders

// TranscriptReaderInfo carries the metadata for a CLI whose on-disk
// conversation transcript the adapter knows how to read back. A provider
// has an entry here if and only if its contract.AdapterReadsTranscript is
// true — the drift test in coding_agent_contract_test.go enforces this so
// the contract can never silently claim a reader exists when none does.
//
// PathTemplate is documentation only (also mirrored in contract.
// TranscriptPathTemplate for discoverability). Actual path resolution
// lives in the adapter packages.
//
// To add transcript-reader support for a new CLI:
//  1. Implement the reader function in the adapter package (e.g.
//     claudecode/claudecode_transcript_usage.go, codexcli/
//     codexcli_transcript_usage.go, cursorcli/cursorcli_transcript_messages.go).
//  2. Register the provider here with the path template.
//  3. Flip contract.AdapterReadsTranscript to true and populate
//     contract.TranscriptPathTemplate with the same string.
//
// Steps 2 and 3 must move together; the drift test fails otherwise.
type TranscriptReaderInfo struct {
	PathTemplate string
}

var transcriptReaderRegistry = map[Provider]TranscriptReaderInfo{
	ProviderClaudeCode: {
		PathTemplate: "~/.claude/projects/*/<session-id>.jsonl",
	},
	ProviderCodexCLI: {
		PathTemplate: "~/.codex/sessions/YYYY/MM/DD/rollout-<timestamp>-<session-uuid>.jsonl",
	},
	ProviderCursorCLI: {
		PathTemplate: "~/.cursor/chats/<md5(cwd)>/<agentId>/store.db",
	},
	ProviderPiCLI: {
		PathTemplate: "$PI_CODING_AGENT_SESSION_DIR/**/*_<session-id>.jsonl or ~/.pi/agent/sessions/**/*_<session-id>.jsonl",
	},
	ProviderMuseCLI: {
		PathTemplate: "$XDG_DATA_HOME/muse/sessions/YYYY/MM/DD/<session-id>/session.jsonl",
	},
	ProviderAgyCLI: {
		PathTemplate: "~/.gemini/antigravity-cli/conversations/<conversation-id>.db",
	},
}

// TranscriptReaderFor returns the transcript reader metadata for a provider,
// or zero-value + false if the provider's adapter does not read transcripts.
func TranscriptReaderFor(provider Provider) (TranscriptReaderInfo, bool) {
	info, ok := transcriptReaderRegistry[provider]
	contract, exists := GetCodingAgentProviderContract(provider, "")
	if !ok || !exists || !contract.AdapterReadsTranscript {
		return TranscriptReaderInfo{}, false
	}
	info.PathTemplate = contract.TranscriptPathTemplate
	return info, true
}
