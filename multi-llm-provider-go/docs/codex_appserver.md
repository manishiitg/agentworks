# Codex app-server transport

Codex runs through `codex app-server --listen stdio://` (tested with CLI 0.164.0).
Each persistent session owns a process and a native thread. Bounded calls close
the process after the turn. No tmux pane or `codex exec` fallback participates in
Codex generation.

The adapter starts or resumes native threads, starts turns, steers the active
turn with `turn/steer`, and interrupts with `turn/interrupt`. Native item events
carry streamed text, reasoning and tool calls. `turn/completed` determines when
a turn finishes. Token and account-limit events provide usage. Session handles
contain the native thread ID and `app-server` transport, with no tmux identity.

Account selection stays with the host. The child receives the selected account's
`CODEX_HOME`, credentials and sandbox policy. A change to the scoped environment
or launch configuration restarts the process. MCP configuration travels through
private stdio as thread config; app-server does not accept the CLI profile flag.
The SDK binds continuation to the selected account. A caller using the adapter
directly remains responsible for authorizing any supplied native resume ID.

The normal native Codex harness remains in use, including shell, skills and MCP
under the configured tool mode and confinement. There is no terminal screen to
mirror. Unsupported interactive server requests receive a protocol error instead
of leaving the stream waiting indefinitely; native question/approval UI is a
separate host integration.

Real-CLI P0 coverage lives in `codexcli_appserver_live_test.go`: native history,
resume, markdown, streaming, slow MCP tools with live steering and user-item
acknowledgement, interrupt/reuse, tool modes, skills, working directory and
parallel isolation. The shared P0 registry selects those tests plus runtime and
plan-usage checks. SDK P0 exercises real bridge multi-turn writes, steering and
concurrent sessions. The server's retained-turn P0 checks native thread identity.

Run the selected provider tests with `-coding-cli-p0-live`; a real authenticated
Codex CLI and Node are required. Model selection uses the existing
`CODEX_CLI_REAL_CONTRACT_MODEL` override. Unit checks do not establish a live
product flow, and concurrent sessions under one login do not certify two distinct
signed-in accounts.
