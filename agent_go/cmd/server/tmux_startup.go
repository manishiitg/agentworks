package server

import "os"

// clearInheritedTmuxEnvironment keeps CLI launches and terminal supervision on
// the same socket. TMUX selects an enclosing terminal's server ahead of
// TMUX_TMPDIR; retaining it makes bare adapter launches escape server isolation.
// Run once after loading startup configuration, before any tmux operations.
func clearInheritedTmuxEnvironment() bool {
	cleared := false
	for _, key := range []string{"TMUX", "TMUX_PANE"} {
		if _, inherited := os.LookupEnv(key); inherited {
			_ = os.Unsetenv(key)
			cleared = true
		}
	}
	return cleared
}
