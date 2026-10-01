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
