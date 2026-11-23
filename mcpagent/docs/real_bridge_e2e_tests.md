# Real-Bridge E2E Tests

## Purpose

The `agent/*_e2e_test.go` suite drives **real coding-agent CLIs** (`claude`, `codex`,
`cursor-agent`, `pi`) through a **real MCP bridge subprocess** — no mocks, no fakes.

These tests exist because coding-agent behaviour cannot be verified any other way:
a mocked CLI cannot reproduce provider-native `--resume`, cwd-keyed conversation
storage, tmux pane lifecycle, or the way a CLI flushes session state on exit.
Every bug this suite has caught was invisible to unit tests.

**They are opt-in and skip by default.** Nothing in [`ci.yml`](../.github/workflows/ci.yml)
or the [`Makefile`](../Makefile) runs them, so a green `go test ./...` says nothing
about any behaviour below.

---

## Running

```bash
# One test (fastest feedback while iterating)
RUN_MCPAGENT_REAL_BRIDGE_E2E=1 go test ./agent/ \
  -run 'TestIsolatedWorkspaceNativeResumeAcrossTurns/Structured/Claude' -v -timeout 15m

# One provider across everything
RUN_MCPAGENT_REAL_BRIDGE_E2E=1 go test ./agent/ -run '.*/Claude' -v -timeout 30m

# The whole suite (minutes, and spends real model tokens)
RUN_MCPAGENT_REAL_BRIDGE_E2E=1 go test ./agent/ -v -timeout 60m
```

| Requirement | Why |
|---|---|
| `RUN_MCPAGENT_REAL_BRIDGE_E2E=1` | The gate. Without it every test `t.Skip`s. |
| Authenticated `claude`, `codex`, `cursor-agent`, `pi` | Each provider case `t.Skip`s if its binary is absent — **a partial run still reports PASS**. |
| `tmux`, `node` | tmux transport and the bridge subprocess. |
| Generous `-timeout` | Real model turns. The default 10m panics mid-suite. |

> **A pass can be a skip.** Provider cases skip individually on a missing binary.
> Always read the `--- PASS/SKIP` lines per subtest rather than trusting the
> package-level `ok`.

---

## The tests

| File | Proves |
|---|---|
| [`isolated_workspace_resume_e2e_test.go`](../agent/isolated_workspace_resume_e2e_test.go) | Native resume works when the session runs in an isolated workspace — across **both** transports. See below. |
| [`coding_session_continuity_e2e_test.go`](../agent/coding_session_continuity_e2e_test.go) | Resume survives losing the tmux pane (crash / idle-eviction / restart). |
| [`structured_transport_multiturn`…](../agent/structured_transport_toolfailure_multiturn_e2e_test.go) | Multi-turn continuity, tool-failure recovery and give-up on structured transport. |
| [`structured_transport_system_prompt_e2e_test.go`](../agent/structured_transport_system_prompt_e2e_test.go) | System prompt + skills survive a NEW `Agent` (structured). |
| [`tmux_system_prompt_skills_e2e_test.go`](../agent/tmux_system_prompt_skills_e2e_test.go) | Same for tmux, plus projected artifacts removed on close. |
| [`real_bridge_multiturn_concurrent_e2e_test.go`](../agent/real_bridge_multiturn_concurrent_e2e_test.go) | Streaming across multi-turn and concurrent sessions. Hosts the shared harness. |
| [`real_bridge_streaming_e2e_test.go`](../agent/real_bridge_streaming_e2e_test.go) | Streaming chunks and markdown fidelity. |
| [`real_bridge_tool_failure_e2e_test.go`](../agent/real_bridge_tool_failure_e2e_test.go) | Tool-failure recovery / give-up over the real bridge. |
| [`streaming_message_modes_e2e_test.go`](../agent/streaming_message_modes_e2e_test.go) | Message-mode behaviour while streaming. |
| [`conversation_recording_e2e_test.go`](../agent/conversation_recording_e2e_test.go) | Recorded turn data matches the real turn. |

### Shared harness

`buildRealBridgeAgent(ctx, tc, tmpBase, workDir, sessionID, persistent)` in
[`real_bridge_multiturn_concurrent_e2e_test.go`](../agent/real_bridge_multiturn_concurrent_e2e_test.go)
boots a real bridge and returns a wired `*Agent` plus cleanup.
`multiTurnProviderCases` is the four-provider table every matrix test ranges over.

`persistent=true` selects the provider's persistent-interactive **tmux** session;
`persistent=false` leaves the process one-shot, which is what structured needs.

---

## TestIsolatedWorkspaceNativeResumeAcrossTurns

Regression cover for the isolated-workspace resume bug. Runs a **2×4 matrix**:
`{Tmux, Structured} × {Claude, Codex, Cursor, Pi}`.

### What broke

Coding CLIs key a resumable conversation by **working directory** (claude stores it
under `~/.claude/projects/<slugified-cwd>/`). `IsolatedSessionWorkspace` gave each
`Agent` a fresh `os.MkdirTemp` — and a **new `Agent` is built for every turn** — so
turn 2 ran in a different cwd, the CLI could not find turn 1's conversation, and the
turn silently restarted as a fresh one:

```
claude run reported an error result:
No conversation found with session ID: 57d1f491-0084-4309-abc1-8485f57b133d
```

`Agent.Close` also `rm -rf`'d the dir between turns, so even a recorded path pointed
at deleted state. Symptom in production: agents lost their memory mid-step, and a
`~/.claude/projects/mlp-cli-session-*` directory leaked per turn.

### Why a code word

The test has turn 1 **speak** a random token, never writing it to any file, then
resumes on a **brand-new `Agent` with empty local history** and asks for it back.
The only channel that can carry the token is genuine provider-native resume — so
recalling it proves resume end to end. A log assertion would not: the failure mode
was a *silent* restart that looks like a healthy turn.

The test also carries the session handle from turn 1 to turn 2
(`CurrentAgentSessionHandle` → `ApplyAgentSessionHandle`), which is exactly what
production does with the persisted handle. Without that step no resume is attempted
at all and the test fails for an unrelated reason.

### Invariants pinned

| Assertion | Guards |
|---|---|
| turn 2 resolves the **same** isolated dir as turn 1 | [`ensureIsolatedWorkspaceDir`](../agent/coding_agent_options.go) derives the path from the session ID instead of minting a random one. |
| dir still exists after `agent1.Close()` | `Agent.Close` must not delete a session-derived workspace — the next turn resumes into it. |
| `handle.Provider.WorkingDir` == isolated dir | The handle records where the conversation actually lives. Catches [`legacyCodingProviderSessionHandle`](../agent/session_handle.go) regressing to `CodingAgentWorkingDir`. |
| `resolveContinuationWorkingDir("")` == isolated dir | Resume never falls back to the user's real workspace. |
| turn 2 recalls the code word | The conversation genuinely resumed. |
| `CloseSession` removes the dir | Session end reclaims the workspace — see `RemoveIsolatedSessionWorkspace`. |

### Why both transports

They fail **differently**, so a pass on one proves nothing about the other:

- **Structured** — no structured adapter attaches a session handle, so
  `legacyCodingProviderSessionHandle` builds one and previously hardcoded
  `CodingAgentWorkingDir`. This is the transport that failed in production.
- **tmux** — the interactive adapters *do* record `WorkingDir`, but it pointed at a
  per-`Agent` random dir that `Agent.Close` then deleted.

The tmux half passed throughout the bug's lifetime. Only the structured half
reproduced the production error.
