# Muse startup under Landlock on Excellence

On 2026-09-30, Excellence's installed Muse Code 1.4.1
(`1.4.1-R4503.1`) exited before accepting a prompt. AgentWorks reported
`muse tmux session ... died while waiting for muse TUI to return to the prompt`.
The underlying stderr was `Agent Definition filesystem source failed: IoError`.
The server had `AGENTWORKS_CLI_LANDLOCK=on`; its three services were running.

An isolated `muse exec --provider echo --trust-workspace --json` prompt
completed without Landlock and failed with that startup error under the
shipped launcher. The filesystem trace showed denied `open("/", ...
O_DIRECTORY)` calls. Both a `.git` boundary and
`agent_definitions.safe_mode=true` left the startup failure unchanged.

## Fix on main

The other agent landed the same directory-only approach in
`multi-llm-provider-go` commit `fcc6a1e` and `mcp-agent-builder-go` commit
`9a9232806`. The provider library emits `list_paths: ["/"]` for confined
CLI launches. The launcher grants those paths only
`LANDLOCK_ACCESS_FS_READ_DIR`, separately from `read_paths` and `write_paths`.
Both components must be deployed together. The duplicate opt-in patch from
this investigation was removed after rebasing onto those commits.

The provider change also permits the shared `/tmp` for CLI sockets and
resolves each account's credential source using its own config-directory
environment. The builder change moves server launch scripts and temporary
configs to a private `TMPDIR`, with `TMUX_TMPDIR=/tmp` retaining the current
tmux socket. These changes and their accepted limitations are recorded in
`docs/DECISIONS.md`.

The final rebase also retains the subsequent provider fixes `ce743f6` and
`51aa9df`: importing the selected pre-confinement session into its private
home on resume, and granting read access to the managed-hook directory.
These address separate resume and `Prompt blocked by hook` failures. The
provider's session-adoption tests and Muse adapter tests pass with this
follow-up on top of those fixes.

**Privacy tradeoff:** Landlock grants are recursive. A directory-only rule on
`/` permits opening/listing directories throughout the host wherever Unix
permissions allow, including the names of files and other users' projects.
It does not grant file-content reads, program execution, or writes. Existing
grants still determine those rights. This workaround must not be described
as merely traversing ancestor directories or as preserving name privacy.
This tradeoff is recorded in the decisions log. Listing includes file names,
not just subdirectory names. Shared `/tmp` is a separate explicit exception
to project isolation in the provider policy.

The runner completed the same isolated echo prompt on Excellence. The exact
startup failure was also reproduced locally in an unprivileged Ubuntu 24.04
ARM64 container on OrbStack with Muse 1.4.1-R4503.1, and the upstream
`list_paths` fix was verified with the same probe.
A canary file outside the explicitly allowed folder could be listed by name
but neither read nor overwritten under the directory-only grant. This tests
the listing grant itself; the production SDK separately grants shared `/tmp`.
No production configuration or services were changed for these probes.
Authentication and a real Meta model turn were not certified by the echo test.

## Narrower follow-up in this worktree

The SDK follow-up retains upstream `list_paths` and removes the blanket shared
`/tmp` grant for **Muse only**. Muse receives a private `TMPDIR` from
`CLIHomeEnvironment`; it does not need Cursor's fixed socket compatibility
grant to start or resume. Other providers retain the upstream grants. Explicit
caller-provided host grants are still honored.

The SDK regression test uses the actual emitted launch policy and a real
Linux runner. Before this follow-up it could read a canary file belonging to
another CLI in shared `/tmp`; after the follow-up both reads and overwrites
are refused. Workspace reads and writes remain allowed. The actual SDK policy
also completed a live Muse echo prompt without the shared `/tmp` grant.

An interactive tmux probe completed a fresh echo turn, killed its own private
tmux server, resumed the saved Muse session, and completed a second turn under
the same tighter filesystem policy. No account credentials were involved.
Full native-tool behavior and Meta authentication/token refresh are not
certified by these echo tests. The follow-up was developed and tested in
owned worktrees. It does not deploy or change the production services.

For name privacy as well as file isolation, Muse needs a loader compatible
with denied ancestor-directory reads, or a private filesystem namespace
that exposes only the granted workspace and runtime. Broad file-read access
on `/` and disabling Landlock are not required by this workaround.

## Local regression probe

Build `workspace/cmd/landlock-runner` for Linux and run:

```sh
python3 scripts/test-muse-landlock-startup.py --runner /absolute/path/to/runner --muse /absolute/path/to/muse --tui
```

The probe uses disposable homes, no login, and the echo provider. It asserts
unconfined startup succeeds, confinement without listing reproduces the
`IoError`, and confinement with `list_paths` succeeds. It also tests denied
canary reads/writes outside the explicit grants and allowed workspace writes.
`--tui` additionally checks interactive startup and native resume using its
own temporary tmux socket.
