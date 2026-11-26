// Package clilaunch lets the host start a coding CLI for a user under the same Landlock confinement the
// chat adapters use, for launches the adapters do not make: the terminals the Providers screen opens so a
// person can sign their own account in or inspect it.
package clilaunch

import (
	"os/exec"

	"github.com/manishiitg/multi-llm-provider-go/internal/clisandbox"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// ConfineCmd rewrites cmd so it starts under the host's Landlock launcher with access to policy's private
// home, workingDir and the CLI's own install, and nothing else of the host's files. cmd.Env must already
// be set (it carries the private HOME). The returned function releases the policy file; call it when the
// command ends. A policy that does not enforce Landlock (no launcher, not Linux, compatibility mode)
// leaves cmd unchanged: the caller decides whether that is acceptable. An enforcing policy whose launcher
// cannot be used returns an error, never an unconfined command.
func ConfineCmd(policy *llmtypes.CLISecurityPolicy, cmd *exec.Cmd, workingDir string) (func(), error) {
	return clisandbox.LandlockCmd(policy, cmd, workingDir, nil, nil)
}
