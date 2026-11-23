package cursorcli

// NativeProjectInstructionFiles declares project files this CLI is configured
// to discover from its working directory. This is capability metadata, not a
// receipt that any particular file was read. Returned slices are caller-owned.
func NativeProjectInstructionFiles() []string {
	return []string{"AGENTS.md"}
}
