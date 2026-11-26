// Package gitcred adds git settings through git's environment configuration (GIT_CONFIG_COUNT/KEY_n/VALUE_n), and
// the environment that lets git and gh act as a person's connected GitHub account (PLAT-746-158 B8).
//
// Several layers set git configuration for one command: the sandbox's excludes file, the slot's protection
// (safe.directory, and hooks and fsmonitor off), a connected GitHub account's credential helper. Every layer appends
// at index GIT_CONFIG_COUNT and raises the count; none skips or resets, or one silently drops the other's settings.
// The one deliberate reset is the GitHub credential helper list (see githubHelperPairs).
package gitcred

import (
	"strconv"
	"strings"
)

// SecretEnv is the environment variable a session's GitHub token arrives in, like every secret.
const SecretEnv = "SECRET_GITHUB_TOKEN"

// credentialHelper answers git's "get" for https://github.com with the token in GH_TOKEN.
const credentialHelper = `!f() { test "$1" = get || exit 0; printf 'username=x-access-token\npassword=%s\n' "$GH_TOKEN"; }; f`

// githubHelperPairs empties git's helper list for github.com before adding ours. Without the empty entry git also
// runs helpers from system config (Homebrew's osxkeychain) and asks them to store the token after a push: from a
// sandboxed CLI with a private home that shows the person a "Keychain Not Found" popup, and elsewhere it copies the
// connected account's token into their keychain.
var githubHelperPairs = []string{"credential.https://github.com.helper", "", "credential.https://github.com.helper", credentialHelper}

// AppendGitConfig returns env with key/value pairs added to git's environment configuration after the entries it
// already has, and the count raised to match.
func AppendGitConfig(env []string, pairs ...string) []string {
	count := 0
	out := make([]string, 0, len(env)+len(pairs)+1)
	for _, kv := range env {
		if value, ok := strings.CutPrefix(kv, "GIT_CONFIG_COUNT="); ok {
			if n, err := strconv.Atoi(value); err == nil && n > 0 {
				count = n
			}
			continue
		}
		out = append(out, kv)
	}
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, "GIT_CONFIG_KEY_"+strconv.Itoa(count)+"="+pairs[i], "GIT_CONFIG_VALUE_"+strconv.Itoa(count)+"="+pairs[i+1])
		count++
	}
	return append(out, "GIT_CONFIG_COUNT="+strconv.Itoa(count))
}

// WithGitHub adds GH_TOKEN and the github.com credential helper to a command's final environment when it carries
// SECRET_GITHUB_TOKEN. Nothing is written to a file.
func WithGitHub(env []string) []string {
	token := ""
	for _, kv := range env {
		if value, ok := strings.CutPrefix(kv, SecretEnv+"="); ok {
			token = value
		}
	}
	if token == "" {
		return env
	}
	env = append(env, "GH_TOKEN="+token)
	return AppendGitConfig(env, githubHelperPairs...)
}

// AddGitHub is WithGitHub for an environment kept as a map (a coding CLI's launch environment); a later layer, such
// as the CLI sandbox, appends its own settings after these.
func AddGitHub(env map[string]string) {
	token := env[SecretEnv]
	if token == "" {
		return
	}
	env["GH_TOKEN"] = token
	count, _ := strconv.Atoi(env["GIT_CONFIG_COUNT"])
	if count < 0 {
		count = 0
	}
	for i := 0; i+1 < len(githubHelperPairs); i += 2 {
		env["GIT_CONFIG_KEY_"+strconv.Itoa(count)] = githubHelperPairs[i]
		env["GIT_CONFIG_VALUE_"+strconv.Itoa(count)] = githubHelperPairs[i+1]
		count++
	}
	env["GIT_CONFIG_COUNT"] = strconv.Itoa(count)
}
