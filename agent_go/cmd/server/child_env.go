package server

import "os"

// minimalChildEnvBase lists the non-secret variables a spawned child may
// inherit. Each is carried over only when set in the parent.
var minimalChildEnvBase = []string{
	"PATH", "HOME", "TMPDIR", "TEMP", "TMP",
	"LANG", "LC_ALL", "LC_CTYPE",
	"TERM", "TZ", "USER", "LOGNAME", "SHELL",
}

// minimalChildEnv builds an explicit environment for a spawned child process:
// the non-secret base above plus caller-supplied KEY=value extras. It never
// inherits the full parent environment, so server secrets and unrelated
// credentials stay out of children. Prefer it over cmd.Env = os.Environ()
// and over leaving cmd.Env nil (which also inherits everything).
func minimalChildEnv(extra ...string) []string {
	env := make([]string, 0, len(minimalChildEnvBase)+len(extra))
	for _, key := range minimalChildEnvBase {
		if value, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+value)
		}
	}
	return append(env, extra...)
}

// passthroughChildEnv copies the named variables from the parent environment
// when set. Use it only for values a child provably needs (a CLI's
// documented API key variable, tmux's socket directory), and prefer
// minimalChildEnv without passthrough everywhere else.
func passthroughChildEnv(names ...string) []string {
	var env []string
	for _, name := range names {
		if value, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+value)
		}
	}
	return env
}
