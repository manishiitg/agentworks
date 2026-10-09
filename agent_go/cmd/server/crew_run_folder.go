package server

import "strings"

// crewRunDirEnv names a Run-mode Crew turn's run folder in its shell.
const crewRunDirEnv = "CREW_RUN_DIR"

// crewRunFolder is the run folder of a Run-mode Crew conversation: <crew>/runs/<conversation>/, the one place the turn
// may write (PLAT-756). Run mode runs what the Crew's owner built and saves what that produces here, as a workflow run
// writes its runs/<run>/ folder; it never changes the Crew itself. A Slack thread or a caller's function conversation is
// one conversation, so follow-up turns find what earlier turns made. "" when there is no Crew or no session.
func crewRunFolder(crewRoot, sessionID string) string {
	root := strings.Trim(strings.TrimSpace(crewRoot), "/")
	var id strings.Builder
	for _, r := range strings.TrimSpace(sessionID) {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			id.WriteRune(r)
		default:
			id.WriteRune('_')
		}
	}
	name := strings.Trim(id.String(), "_")
	if root == "" || name == "" {
		return ""
	}
	if len(name) > 96 {
		name = name[:96]
	}
	return root + "/runs/" + name + "/"
}

// crewRunFolderEnv is the shell environment naming a run folder, empty when the turn has none.
func crewRunFolderEnv(runFolder string) map[string]string {
	if runFolder == "" {
		return nil
	}
	path := cliPolicyPath(runFolder)
	if path == "" {
		path = runFolder
	}
	return map[string]string{crewRunDirEnv: path}
}
