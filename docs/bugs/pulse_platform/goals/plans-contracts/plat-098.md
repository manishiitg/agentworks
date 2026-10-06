[← goals / plans-contracts](index.md)

# PLAT-098 — contract upgrades were invisible to the workflow's owner, and unrunnable by hand

| Field | Value |
|---|---|
| State | open |
| Priority | P1 |
| Product | goals |
| Area | plans-contracts |
| Summary | P1 operability — a workflow could sit blocked indefinitely with |

| Coordination | Value |
|---|---|
| Assigned agent | Claude Code |
| Ticket state | `done` |
| Last synchronized | `2026-08-12` |

- **Priority:** P1 operability — a workflow could sit blocked indefinitely with
  no way for its owner to see what was being asked, or to complete it manually.
- **Observed on:** `Workflow/confida-login`, blocked at contract `1.0.20` for
  days while the platform current was `1.0.25`.

## What an owner could see

One line, in run history:

```text
workflow upgrade preflight upgrade-current-artifact-contract did not stamp
required version "1.0.21" (found "1.0.20", failure 2/3 consecutive)
```

That is a version and a counter. Not what the migration asks for, not which
rungs are queued behind it, not why the last attempt stopped.

Everything else was unreachable. The instructions are Go constants delivered
only by the scheduler — no tool, no API, and `grep contract_version frontend/src`
returns nothing. The refusal reasoning lives in that scheduled session's own
transcript under `builder/conversation/`. Diagnosing confida-login meant reading
server logs and conversation JSON by hand for a full day.

## What an owner could do

Nothing supported. `set_workflow_contract_version` is registered in every
workshop session, so pasting the instruction into the builder chat happened to
work — undocumented, and it depended on the operator already having the text
they could not obtain.

PLAT-096 then closed even that by accident. Its fence required a scheduler grant
for every stamp, which is correct for a scheduled session and wrong for a human:
an operator could do the whole migration in the builder and be unable to record
it. **The one manual route out of a stuck upgrade was removed by the fix for the
stuck upgrade.**

## Fix

- **`get_contract_upgrades`** (workshop tool): current version, every pending
  rung in order with the full instruction text, and recorded failures per
  schedule with their consecutive counts and fail-open state. A version this
  server has no path from is called out explicitly — every scheduled run refuses
  to start on it, which is the least self-evident failure in the subsystem.
  The instruction text is included verbatim rather than summarized: an owner
  judging whether a stalled migration is safe needs the actual words.
- **The stamp fence now binds only scheduler-owned sessions.** The scheduler
  claims its session for the whole run — preflight, schedule messages, Pulse —
  and releases it at the end, dropping any unspent grant. An operator in the
  builder is authorized by being there: they asked for the migration and can see
  what the agent does.
- **An operator stamp is checked against the ladder.** With no grant pinning a
  target, nothing else stopped a stamp jumping to the newest version and marking
  three migrations done that never ran — the PLAT-096 defect reintroduced
  through the manual door. Only the next pending rung is accepted. A ladder
  lookup failure does not block: an infrastructure hiccup must not lock the only
  manual route.
- **The stamp tool's description named only the scheduler** as its caller, so an
  agent following instructions would have declined in an operator's chat even
  with the fence relaxed. It now names both callers and states that migrations
  are stamped one at a time, after their work.

## The tool shipped unusable

`get_contract_upgrades` was registered but never added to
`GetToolsForWorkshopMode`, so its first real call returned:

```text
tool_not_allowed: "get_contract_upgrades" is registered but is not in this
session's allowed tool set. This is a fixed grant for this agent, not a
transient failure ... retrying, renaming, or changing settings will NOT make
it callable.
```

The agent correctly stopped trying and fell back to curling the endpoint, which
failed identically. Nothing catches this at build time, and the message reads as
a deliberate withholding rather than a misconfiguration.

`TestEveryRegisteredWorkshopToolIsAllowedInSomeMode` now parses
`interactive_workshop_manager.go` and fails when a registered tool is in no
mode's allow-list. Confirmed it catches the real defect by removing the entry:

```text
registered but in no workshop mode's allowed set, so unusable at runtime: [get_contract_upgrades]
```

The existing `TestToolSetInvariants` already pinned the mirror direction —
allow-listed tools must have a real registration path — and also failed until
the name was added there. The two together close the loop.

## Verification boundary

Source coverage proves the status output names every pending rung and carries
the instruction text, stays quiet at the current contract, explains an unknown
version, and that an operator stamp is accepted for the next rung, refused for a
later one, refused when nothing is owed, and survives a lookup failure.

Live proof is an operator opening the builder on a blocked workflow, reading the
pending migration, completing it, and stamping — which is now the supported
route for confida-login while the scheduled path remains blocked (PLAT-096).

## Related

- PLAT-096 — the delivery and authorization defects in the same subsystem. Its
  fence is what removed the manual route this ticket restores, and its scheduled
  path is still blocked on confida-login.
