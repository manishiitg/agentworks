[← brain / pulse-governance](index.md)

# PLAT-156 — `review_plan` injects a 553 KB plan and a Go-authored semantic summary instead of letting the reviewer inspect authoritative files

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | brain |
| Area | pulse-governance |
| Summary | P1 — the prompt can exceed practical context budgets and the |

| Coordination | Value |
|---|---|
| Assigned agent | Codex |
| Ticket state | `implemented` — focused tests pass; live Pulse reverify pending |
| Last synchronized | `2026-08-19` |

- **Priority:** P1 — the prompt can exceed practical context budgets and the
  reviewer receives a controller-selected interpretation before it reads the
  source it is supposed to audit.
- **Owner:** `interactive_workshop_manager.go` (`runReviewPlanAgent`,
  `reviewPlanAgentSystem`)

## Evidence and RCA

The Social Media `planning/plan.json` observed during the 2026-08-19 run is
553,349 bytes. `runReviewPlanAgent` read that entire file in Go, generated a
second prose summary of every step configuration in Go, and inserted both into
the static reviewer prompt. The reviewer therefore paid the full context cost
before choosing what it needed, and its view was partly shaped by controller
logic (`mode`, locks, learning/KB flags) rather than by its own inspection of
the authoritative files.

This also explains why large plans encourage repeated broad searches: the
reviewer begins with a huge flattened prompt instead of navigating one route or
contract at a time.

## Fix and reasoning

The controller now supplies only execution facts it owns: workspace path,
optional run folder, focus, and session identity. The reviewer is explicitly
required to read:

- `soul/soul.md` for objective and success criteria;
- `workflow.json` for selected skills and capability settings;
- `planning/plan.json` with targeted `jq` queries rather than a full dump;
- `planning/step_config.json` for modes, tools, skills, locks, and store access.

Go still owns deterministic safety boundaries (read-only tool surface, folder
guard, workspace identity, lifecycle, and schema parsing). It no longer
pre-interprets the workflow design for the semantic reviewer.

## Acceptance

1. A sentinel passed as `PlanJSON` or `StepConfigSummary` cannot appear in the
   rendered review prompt.
2. The prompt directs the agent to inspect all authoritative sources itself.
3. The reviewer retains read-only filesystem and managed DB access.
4. A live Pulse plan review of Social Media completes without a full-plan prompt
   injection and still cites exact step/route evidence.
