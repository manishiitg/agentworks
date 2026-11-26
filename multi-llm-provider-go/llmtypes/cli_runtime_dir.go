package llmtypes

import (
	"fmt"
	"path/filepath"
	"strings"
)

// WithCLIRuntimeDir selects application-owned storage for generated provider
// files. The application must supply a private directory admitted for this turn.
func WithCLIRuntimeDir(dir string) CallOption {
	return func(o *CallOptions) { o.CLIRuntimeDir = strings.TrimSpace(dir) }
}

func CLIArtifactDirectory(o *CallOptions, workingDir string) string {
	if o != nil && strings.TrimSpace(o.CLIRuntimeDir) != "" {
		return strings.TrimSpace(o.CLIRuntimeDir)
	}
	return workingDir
}

func CLIHasPrivateRuntime(o *CallOptions) bool {
	return o != nil && strings.TrimSpace(o.CLIRuntimeDir) != ""
}

// Private projections are deliberately outside native project discovery. Tell
// the agent where to read them without modifying the project's own guidance.
func CLIPrivateSkillInstructions(o *CallOptions, subdir string) string {
	if !CLIHasPrivateRuntime(o) || len(AttachedSkillsFromOptions(o)) == 0 {
		return ""
	}
	return fmt.Sprintf("\nAgentWorks skills for this turn are stored at %q. Read each relevant skill's SKILL.md there, or use read_skill. Keep generated instructions and configuration in the private runtime; preserve the project's existing instructions and configuration.\n", filepath.Join(o.CLIRuntimeDir, subdir))
}

// WithCodingProviderTransport preserves the runtime of an admitted native handle.
// New sessions use the provider's configured transport when this is empty.
func WithCodingProviderTransport(transport string) CallOption {
	return func(o *CallOptions) { o.CodingProviderTransport = strings.TrimSpace(transport) }
}
