package slots

import "encoding/json"

// ExecRequest is what the platform sends to `slotctl exec` on its standard input.
type ExecRequest struct {
	// Argv is the program and arguments. Argv[0] must be on slotctl's allow-list.
	Argv []string `json:"argv"`
	// Cwd is the working folder; it must lie inside slotctl's allowed folders.
	Cwd string `json:"cwd"`
	// Env is the complete environment of the program (the platform builds it; nothing is inherited).
	Env []string `json:"env"`
	// Userns starts the program in its own user and mount namespaces (the Landlock launcher's
	// private /tmp view), created after the switch to the slot.
	Userns bool `json:"userns,omitempty"`
	// FD3 is handed to the program as file descriptor 3 (the Landlock policy), never as a file.
	FD3 string `json:"fd3,omitempty"`
}

// SudoArgv is the command that runs slotctl as the slot.
func SudoArgv(slot string) []string {
	return []string{DefaultSudo, "-n", "-u", slot, Slotctl(), "exec"}
}

func encode(req ExecRequest) ([]byte, error) {
	return json.Marshal(req)
}

// GitSlotEnv is the git configuration every command run as a slot gets. The folders belong to the
// platform account with the slot's group, so git would refuse them as "dubious ownership"; that check
// is switched off (safe.directory=*). It also guarded against a repository whose own configuration runs
// code, so the two ways a repository does that without being asked, hooks and a file-system monitor,
// are switched off too (core.hooksPath, core.fsmonitor). A shared folder's author cannot get code run
// as another user's slot through git. Recorded in docs/DECISIONS.md.
func GitSlotEnv() []string {
	return []string{
		"GIT_CONFIG_COUNT=3",
		"GIT_CONFIG_KEY_0=safe.directory", "GIT_CONFIG_VALUE_0=*",
		"GIT_CONFIG_KEY_1=core.hooksPath", "GIT_CONFIG_VALUE_1=/dev/null",
		"GIT_CONFIG_KEY_2=core.fsmonitor", "GIT_CONFIG_VALUE_2=false",
	}
}
