[← goals / learnings](index.md)

# PLAT-168 — after a successful `query_workflow_db` call, an agent redundantly hand-rebuilt the same HTTP request and burned 45 minutes fighting its own Python quoting

| Field | Value |
|---|---|
| State | open |
| Priority | P2 |
| Product | goals |
| Area | learnings |
| Summary | P2 — not a platform reliability bug (the 45m ceiling from |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `filed` — root cause identified from a live incident; also records a real-time-detection design decision reached the same day, so it is not re-litigated |
| Last synchronized | `2026-08-21` |

- **Priority:** P2 — not a platform reliability bug (the 45m ceiling from
  [PLAT-153](../../coding-agents/pi/plat-153.md)) caught it and recovered cleanly, exactly as
  designed). The cost is wasted turn time and tool-call spend on a step that
  never needed to fail at all.
- **Owner:** step/skill guidance for agentic steps that use `query_workflow_db`
  (`agent_go/cmd/server/guidance/templates/system/stores.md` and any per-step
  prompt built on it) — not adapter or orchestrator code.

## The incident

2026-08-21, workflow `Mututal-Fund`, group `sanjayhuf`, sub-agent session
`sub-exec-eval-sync-integrity-1787293000928073000` (pi-cli structured,
`google/gemini-3.7-flash`). Killed by PLAT-153's 45-minute turn ceiling —
`llm_duration=2700025ms`, exactly 45m0.025s — after 212 tool calls that never
converged.

This was reported live as "is the workflow stuck again," and the first-pass
read (a wedge, or a runaway) was wrong. Reading the actual transcript found a
specific, narrow, fixable cause:

1. `11:47:00` — the session called `query_workflow_db` **directly and
   correctly**, as the registered native tool it is
   (`stores.md`: *"Agentic steps ... inspect schemas and read through
   `query_workflow_db`"*). No error was logged for this call.
2. `11:47:11` onward — the same session then tried to **rebuild the identical
   HTTP request by hand**, inside `execute_shell_command`, writing inline
   Python against `$MCP_CUSTOM/query_workflow_db` with `urllib.request`. The
   first attempt failed immediately:
   `SyntaxError: unterminated triple-quoted string literal (detected at line
   17)` — the model tried to embed a multi-line Python script inside a
   triple-quoted string passed through a shell one-liner, and the quoting
   broke across the shell/Python boundary.
3. Rather than falling back to the tool it had already used successfully (or
   the simpler `curl` one-liner it also tried once early on), it kept
   retrying **variations of the same fragile pattern** — plain `urllib`, a
   base64-encoded script, manual auth-token extraction — each one a new way
   to smuggle a multi-line script through a shell string, each with its own
   way to break. 19+ of these retries failed with `exit_code=1`, spread
   across the full 45 minutes, never more than ~90s apart.

The tool that was needed had already been called and had already worked. The
entire failure was redundant re-verification of a result the agent already
had.

## A design decision reached the same day, recorded here so it isn't re-argued

Discussed at length while diagnosing this incident: should the platform try to
detect *this specific pattern* — active, never-silent, but not converging —
in real time, and kill it faster than the 45-minute ceiling?

**Decided against it.** A real-time "productive iteration vs. unproductive
loop" classifier is the wrong bet:

- Agents legitimately retry and adjust as normal problem-solving. A session
  on its 6th failed attempt that is about to succeed on its 8th looks
  **identical**, from the outside, mid-run, to a session that will never
  succeed. There is no reliable live signal that tells them apart.
- A consecutive-failure or failure-rate circuit breaker would create real
  false positives — killing sessions that were genuinely about to converge —
  in exchange for shaving minutes off a failure mode the flat ceiling
  already recovers from safely.

**The right split, reached explicitly:**

- **Real time stays dumb.** The turn ceiling ([PLAT-153](../../coding-agents/pi/plat-153.md))) is a
  resource/cost cap, not a quality judgment — it should not try to
  distinguish good iteration from bad. Its only job is to bound total spend
  (time, tool-call cost, a held workflow slot) so nothing runs unbounded,
  regardless of whether the activity inside it is "productive."
- **Diagnosis happens after the fact, with the full transcript.** This is
  what actually found the redundant-curl-reconstruction pattern — a live
  kill-switch could not have identified *why* it was failing, only *that* it
  was taking long. Root-causing and fixing recurring patterns like this one
  is Pulse's job (the same process this whole ticket register already is),
  not a real-time control's job.

No code change follows from this decision — it is a scope boundary, not a
fix. It exists so a future incident like this one is not met with a proposal
to build a failure-pattern detector that was already considered and rejected,
for a specific, load-bearing reason.

## What actually needs fixing

Guidance-level, not code: `stores.md`'s existing instruction (*"read through
`query_workflow_db`"*) states the right tool but does not say the query's
result is authoritative and needs no re-verification. Whatever pattern led
the model to distrust or duplicate its own successful tool call should be
addressed at that level — either by strengthening the guidance explicitly
("the tool result is authoritative; do not re-issue the same query via shell
or curl"), or, if this recurs on other steps, by understanding what in the
step's own task framing (this was an *evaluation* step, whose job is
cross-checking data — plausibly primed toward re-verifying rather than
trusting a single read) prompts the redundant check in the first place.

Not attempted in this pass — this ticket records the finding and the ceiling
design decision; the guidance change itself is a small, separate follow-up.

## Acceptance

- `stores.md` (or the calling step's own prompt) states plainly that a
  `query_workflow_db` result is authoritative and does not need re-verification
  via `execute_shell_command`/`curl`/inline Python.
- If the same redundant-reconstruction pattern recurs on another step after
  that change, escalate past a guidance fix — it would mean the cause is
  task-framing (eval steps specifically), not a missing instruction.
