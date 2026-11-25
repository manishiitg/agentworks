package agycli

// NativeProjectInstructionFiles declares project files this CLI is configured
// to discover from its working directory. This is capability metadata, not a
// receipt that any particular file was read. Returned slices are caller-owned.
// agy 1.5.17 reads the root AGENTS.md and GEMINI.md itself, in print mode and
// over ACP, and ignores CLAUDE.md (probed with marker files, 25 Nov 2026).
func NativeProjectInstructionFiles() []string {
	return []string{"AGENTS.md", "GEMINI.md"}
}
