package server

import (
	"errors"
	"os/exec"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/clilaunch"

	"github.com/manishiitg/coding-agent-loop/workspace/security"
)

// confineProviderSetup starts a Providers-screen terminal for a person's own account under the same Landlock
// confinement as their chat CLIs: files reachable are the account's private home, the folder it starts in and
// the CLI's own install, so the terminal cannot read another user's folders or the server's secret files.
// Before this, the terminal ran the coding CLI as the platform account with only the CLI's own flags
// (--sandbox read-only, --disable-shell) between a user and every file that account can read, and Pi and Agy
// opened unrestricted (docs/DECISIONS.md, 2026-10-01). Only a personal account is confined; the server's own
// account is administered by an admin and keeps its service home.
//
// userAccount says the terminal belongs to a personal account (it has its own private HOME). On a host that
// cannot confine (no Landlock) a multi-user server refuses the terminal instead of running it open; a
// single-user install (desktop) is unchanged. The returned function releases the policy file.
func confineProviderSetup(cmd *exec.Cmd, provider string, environment []string, userAccount bool) (func(), error) {
	noop := func() {}
	if !userAccount {
		return noop, nil
	}
	home := ""
	credentialEnv := make(map[string]string)
	for _, entry := range environment {
		if key, value, ok := strings.Cut(entry, "="); ok {
			credentialEnv[key] = value
		}
		if value, ok := strings.CutPrefix(entry, "HOME="); ok && strings.TrimSpace(value) != "" {
			home = strings.TrimSpace(value)
		}
	}
	runner, available := security.CLILandlockRunner()
	if !available {
		if IsMultiUserMode() {
			return noop, errors.New("terminals for personal accounts are unavailable: this host cannot confine them")
		}
		return noop, nil
	}
	if home == "" {
		return noop, errors.New("personal account terminal has no private home")
	}
	policy := &llmtypes.CLISecurityPolicy{
		Provider:       provider,
		Mode:           llmtypes.CLISecurityModeIsolated,
		LandlockRunner: runner,
		PrivateHome:    home,
		// Setup operates on this account itself. An omitted source falls back
		// to the server home and links the personal login to the shared login.
		CredentialHome: home,
		CredentialEnv:  credentialEnv,
	}
	return clilaunch.ConfineCmd(policy, cmd, cmd.Dir)
}
