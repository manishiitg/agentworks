[← coding-agents / tools](index.md)

# PLAT-180 — a retained (tmux-delivered) turn's tool-call events carried no turn_id at all, because the code path that emits them runs with a bare context that never carries the session's turn identity

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | coding-agents |
| Area | tools |
| Summary | P2 — surfaced as a test-contract failure (IC-11's |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `fixed` — confirmed live for pi-cli, codex-cli, and claude-code |
| Last synchronized | `2026-08-22` |

- **Priority:** P2 — surfaced as a test-contract failure (IC-11's
  `assertCanonicalRetainedTurnIdentity`), not as a user-visible defect. The
  actual downstream consequence of a missing `turn_id` on a
  `tool_call_start`/`tool_call_end` event — wrong grouping in a trace/cost
  view, a UI element attributed to the wrong turn, or genuinely nothing
  anyone currently reads — has not been assessed. Raise if evidence of real
  impact turns up.
- **Owner:** `mcpagent/agent/tool_registry.go` (`observedDirectToolExecutor`,
  the actual fix); `mcpagent/agent/turn_session.go` (`Session.ActiveTurnID`,
  a real but insufficient-alone first attempt);
  `agent_go/pkg/agentwrapper/llm_agent.go` (same).
- **Related:** [PLAT-179](plat-179.md) — this surfaced immediately after
  PLAT-179's fix let the retained-window test progress far enough to reach
  the check this ticket is about. Not reachable before that fix landed.

## How this surfaced

Once [PLAT-179](plat-179.md) was fixed and confirmed live for pi-cli, the
`--retained-window-p0-only` E2E test progressed past the premature-completion
check and hit a new failure:

```
Error: IC-11 retained-window P0 failed: retained execute_shell_command
tool_call_start has no stable turn_id
```

`assertCanonicalRetainedTurnIdentity` (the E2E test's own check) requires
every event belonging to one retained turn — the `unified_completion` and
every `tool_call_start`/`tool_call_end` for the tool that turn ran — to carry
the SAME, non-empty `turn_id`. The completion event has one. The tool-call
events do not.

## First diagnosis — real fix, but not the one this test needed

The first pass (code-reading only, no live event inspection) theorized two
disconnected turn-identity systems: `agent_go`'s `toolcalllog.RegisterHook`
closure, capturing a turn ID once per `AskWithHistory` call and never
refreshed for a later retained delivery, versus `mcpagent`'s own
`canonicalTurnLifecycle` created fresh per retained delivery. The fix shipped
for that theory — `mcpagent`'s `Session.ActiveTurnID()` plus `agent_go`'s
`LLMAgentWrapper.currentTurnID()`, resolving the hook's turn ID live instead
of from a closure — is real, correct, and worth keeping: it does fix a
genuine staleness risk in that hook. **It is not, however, what the live P0
test needed.** Re-running `--retained-window-p0-only` against pi-cli after
shipping it reproduced the exact same failure, unchanged.

The tell was in the actual error text once compared literally:
`assertCanonicalRetainedTurnIdentity` reports `"no stable turn_id"` only when
the field is a **literal empty string** (`turnID == ""`, checked before any
comparison against the completion event's ID). The first diagnosis assumed a
real-but-wrong value (the earlier turn's ID); live behavior was total
absence. That gap — assumed-wrong vs. actually-absent — was the signal that
the wrong code path had been fixed.

## Real root cause

`toolcalllog.RegisterHook`'s hook — the mechanism the first fix touched — is
not even the source of the tool-call events the E2E test observes for a
retained turn. Proof: the observed `ToolCallStartEvent.ToolCallID` in the
server log carries a `direct-<uuid>` prefix, but `toolcalllog.RecordStart`
generates IDs as `toolu_<n>`. Different mechanism entirely.

The real source is `mcpagent/agent/tool_registry.go`'s
`observedDirectToolExecutor` — deliberately built (per its own existing
comment) to emit `ToolCallStartEvent`/`ToolCallEndEvent` for exactly the
retained-turn case, specifically *because* the CLI transcript reconstruction
that covers an active `Run` has no equivalent for a turn delivered directly
into an already-running provider between Runs:

```go
func (a *Agent) observedDirectToolExecutor(name string, executor ToolExecutor) ToolExecutor {
	return func(ctx context.Context, args map[string]interface{}) (string, error) {
		if a.isTurnInFlight() {
			return executor(ctx, args)  // active Run: transcript already covers it
		}
		// retained turn: this function is the ONLY tool-call event source
		callID := "direct-" + uuid.NewString()
		start := events.NewToolCallStartEvent(...)
		a.emitTypedEvent(ctx, start)
		...
	}
}
```

`emitTypedEvent` only stamps `turn_id` metadata when the `ctx` it receives
carries a `canonicalTurnLifecycle`:

```go
func (a *Agent) emitTypedEvent(ctx context.Context, eventData events.EventData) {
	if lifecycle := canonicalTurnLifecycleFromContext(ctx); lifecycle != nil && !lifecycle.prepareEvent(eventData) {
		...
	}
	// lifecycle == nil -> prepareEvent never called -> turn_id never stamped, at all
	...
}
```

The `ctx` `observedDirectToolExecutor` actually receives is a bare
HTTP-request-derived context: the coding CLI's subprocess calls the tool over
HTTP, into `mcpagent/executor/handlers.go`'s custom-tool handler, which builds
`ctx` from `r.Context()` and passes it straight through
`codeexec.CallCustomToolWithSession` into the executor — a completely
different call stack from the one `Session.Send` built its
`canonicalTurnLifecycle`-bearing context on. That lifecycle never reaches this
context. `canonicalTurnLifecycleFromContext(ctx)` returns `nil`, `prepareEvent`
is never called, and `turn_id` is never written to the event's metadata at
all — not the wrong value, no value.

## Fix

`mcpagent/agent/tool_registry.go` gained `Agent.attachActiveTurnLifecycle`,
called once at the top of `observedDirectToolExecutor`'s retained branch,
before any event is built:

```go
func (a *Agent) attachActiveTurnLifecycle(ctx context.Context) context.Context {
	if canonicalTurnLifecycleFromContext(ctx) != nil {
		return ctx
	}
	session, ok := LookupSession(a.sessionID)
	if !ok || session == nil {
		return ctx
	}
	lifecycle := session.currentTurnLifecycle()
	if lifecycle == nil {
		return ctx
	}
	return withCanonicalTurnLifecycle(ctx, lifecycle)
}
```

```go
if a.isTurnInFlight() {
	return executor(ctx, args)
}
ctx = a.attachActiveTurnLifecycle(ctx)
```

`currentTurnLifecycle()` (`turn_session.go`) is the unexported counterpart to
the already-shipped `ActiveTurnID()`, returning the actual
`*canonicalTurnLifecycle` object (not just its ID string) so
`withCanonicalTurnLifecycle` attaches the SAME object `Session` itself uses
for `unified_completion` — same identity, no duplication of the lifecycle's
own dedup/terminal bookkeeping.

This is, again, structurally provider-agnostic: `observedDirectToolExecutor`
and the HTTP custom-tool handler are shared code with no provider branching,
so the fix applies identically to every provider that uses retained delivery.

The earlier `Session.ActiveTurnID()` / `LLMAgentWrapper.currentTurnID()`
change (see "First diagnosis" above) is kept — it is a real correctness
improvement for `toolcalllog`'s hook, independent of this fix, even though it
turned out not to be what this specific test failure needed.

## Verification

- `mcpagent/agent/tool_registry_uniqueness_test.go`:
  `TestDirectToolExecutionEventsCarryTheSessionsActiveTurnID` drives
  `observedDirectToolExecutor` directly with a deliberately bare
  `context.Background()` (exactly what the real HTTP call site passes) against
  a session with a real active `canonicalTurnLifecycle`, and asserts both the
  emitted start and end events carry that lifecycle's ID in their metadata.
  Confirmed failing before this fix — the events carried no `turn_id` key at
  all — and passing after.
- `go build ./...` clean. Full `agent` suite passes except the pre-existing,
  unrelated `TestAgentReviewsApproved` (a stale local testdata-review gate,
  present identically before this work).
- **Confirmed live end-to-end**, dev server running, real
  `--retained-window-p0-only` runs after restarting the server with the fix:
  ```
  provider=pi-cli      PASS coding agent retained-window P0
  provider=codex-cli   PASS coding agent retained-window P0
  provider=claude-code PASS coding agent retained-window P0
  provider=cursor-cli  PASS coding agent retained-window P0
  ```
  pi-cli, codex-cli, and claude-code use tmux/retained delivery in real
  product usage. cursor-cli's product default is structured transport
  (`codingAgentUsesStructuredTransport`, deliberate) — reproduced and
  reconfirmed via the same temporary, env-gated `AGENTWORKS_CURSOR_FORCE_TMUX_TEST`
  bypass PLAT-179's investigation used, restarted with it on, then reverted to
  a clean `git checkout --` (confirmed zero diff) and the server restarted
  again on production defaults before continuing. Every one of the four
  retained-capable providers is now live-confirmed.
  Re-running the exact same command against the unpatched server (before the
  restart) reproduced the original failure identically, confirming the fix —
  not an unrelated server-state change — is what turned it green.

## Deliberately out of scope in this pass

- **Assessing downstream impact.** Whether anything currently reads
  `tool_call_start`/`tool_call_end`'s `turn_id` in a way that its previous
  absence caused visible harm (versus it being an unused/lightly-used
  bookkeeping field) was not checked.

## Acceptance

- [x] Root cause confirmed with the exact mechanism, not inferred from code
      reading alone — traced to `observedDirectToolExecutor`'s bare context
      and confirmed via the `direct-` vs `toolu_` ID-prefix mismatch, then via
      a live server restart and re-run.
- [ ] Downstream consumers of `tool_call_start`/`tool_call_end`'s `turn_id`
      identified, and real impact (if any) assessed.
- [x] A fix design that resolves the cross-repo (`agent_go`/`mcpagent`)
      boundary cleanly — not a per-provider patch, since the root cause is
      shared, provider-agnostic code.
- [x] Confirmed for claude-code, codex-cli, and cursor-cli, not only pi-cli.
- [x] Re-run `--retained-window-p0-only` live against a real coding-agent
      session to confirm `assertCanonicalRetainedTurnIdentity` now passes.
      Done for all four retained-capable providers: pi-cli, codex-cli,
      claude-code, cursor-cli (the last via the temporary tmux-forcing test
      bypass, since cleanly reverted).
      Done for pi-cli, codex-cli, and claude-code.
