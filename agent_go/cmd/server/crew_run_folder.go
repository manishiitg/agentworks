package server

import "strings"

// crewOutputDirEnv names a Crew's output folder in a turn's shell: the same variable and the same folder for the owner
// and for everyone running the Crew. crewRunDirEnv is its earlier name, kept so scripts written for it keep working.
const (
	crewOutputDirEnv = "CREW_OUTPUT_DIR"
	crewRunDirEnv    = "CREW_RUN_DIR"
)

// crewOutputFolder is a Crew's output folder, <crew>/outputs/ (PLAT-812): where a function or script saves what it
// produces (evidence, recordings, reports, results). It is the one place a Run-mode turn may write, so an owner's
// function behaves the same for every caller and the owner finds the results in one place. "" when there is no Crew.
func crewOutputFolder(crewRoot string) string {
	root := strings.Trim(strings.TrimSpace(crewRoot), "/")
	if root == "" {
		return ""
	}
	return root + "/outputs/"
}

// crewOutputFolderEnv is the shell environment naming the output folder, empty when the turn has none.
func crewOutputFolderEnv(folder string) map[string]string {
	if folder == "" {
		return nil
	}
	path := cliPolicyPath(folder)
	if path == "" {
		path = folder
	}
	return map[string]string{crewOutputDirEnv: path, crewRunDirEnv: path}
}
