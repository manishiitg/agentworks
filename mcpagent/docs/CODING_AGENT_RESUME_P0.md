# Coding-agent resume P0

Run `make test-p0-resume` from mcpagent. This credential-free, race-enabled
suite also runs in the **Coding-agent resume P0 / P0 resume directory
persistence** GitHub Actions check on pull requests and pushes.

The test is `agent/session_working_dir_p0_test.go`. It enumerates the production
coding-provider registry and fails if a provider has no adapter-key expectations.
It covers Claude, Codex, Cursor, Pi, and Muse with builder and isolated step
working directories, for tmux and structured handles.

Each case checks the complete shared-layer path:

1. Receive a provider result that omits the working directory.
2. Preserve the known launch directory and native session ID.
3. Serialize the handle to JSON and restore it into a fresh Agent.
4. Invoke the production continuation API with a recording model.
5. Assert that the adapter receives the original directory, original native ID,
   and one current user message.

The suite also checks that explicit provider directories are respected and a
different provider cannot inherit another provider's directory.

This check verifies application persistence and resume-option routing. It does
not launch vendor CLIs or prove native model memory survives a process restart.
Provider live continuity tests are the separate check for that behavior. Muse's
`TestMuseResumeHandleRoundTripTmuxP0` additionally verifies real CLI restart,
same native ID, and fallback history using its no-cost echo provider.

The workflow runs the check; making it a merge requirement also depends on the
repository's branch-protection/ruleset configuration.
