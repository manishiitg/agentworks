[← goals / pulse-governance](index.md)

# PLAT-305 — Separate QA, Architecture and Strategy and close the improvement loop

| Field | Value |
|---|---|
| State | open |
| Priority | - |
| Product | goals |
| Area | pulse-governance |
| Summary | implements independent review roles, sequential scheduling with protected research dates, authorized research access, linked decision/application/outcome tracking, and the revised Pulse workspace. |

> **2026-09-10 update:** [PLAT-306](plat-306.md) supersedes mandatory Markdown
> checkpoint/report maintenance with optional SQLite review notes on the existing
> result tool. Typed lifecycle records remain authoritative; historical design
> discussion below is retained. No reporting-only turn is required.

| Coordination | Value |
|---|---|
| Assigned agent | Codex |
| Ticket state | `implemented role/UI/cadence follow-through locally; deployment and live workflow acceptance pending` |
| Last synchronized | `2026-09-17` |

## User problem

Technical Review mixes bug repair with architectural improvement. Recurring
platform defects occupy review attention while opportunities in learning, KB,
reports, databases, scripts and orchestration receive little independent focus.
Strategy needs internal/external investigation and tracked outcomes rather than
only reading the existing plan/dashboard. Approval, application and impact must
be distinguishable.

## Implemented

- Canonical Architecture module, focus history, finding/decision ownership,
  reports and research guidance; historical identities remain intact.
- Independent due decisions and sequential module stages with their own receipts
  and recovery. Protected Architecture/Strategy dates persist across Gate skips;
  explicit dated deferrals remain possible. See PLAT-303.
- Typed research scope on background dispatch: inherited authorized MCP/browser
  setup, research and Pulse custom tools, scoped checkpoint writes, no workflow
  edit/production execution custom tools. External connection grants are preserved;
  there is no new universal external read-only proxy.
- Architecture improvement records reuse the existing impact ledger. Decision
  answers and application receipts atomically advance linked Architecture and
  Strategy proposals. Architecture application/adoption require appropriate
  application/outcome evidence; applied records cannot reset to proposed.
- Health / Architecture / Strategy UI with separate Drift Check and compact
  improvement-to-outcome history. Full reports remain readable.

## Acceptance

Automated tests cover all four modules due, independent completion after QA
failure, protected boundary persistence, research tool filtering, Architecture
coverage and finding ownership, and proposal/approval/application/assessment/
adoption transitions. Frontend tests cover areas, filters, history, and honest
outcome presentation. Full affected Go packages, focused frontend tests and
production frontend build pass; see the implementation document for exact scope.

## Remaining / not claimed complete

Deploy and observe an actual autonomous workflow improvement. Finish immutable
partial-run evidence under PLAT-047/089 and builder contract/learning prevention
under PLAT-257/298. An automated lifecycle fixture is not evidence of business
impact. No legacy workflow schema or learning file is rewritten by this change.

[Implementation and operational contract](../../../../pulse-workflow-improvement-system.md)

## 2026-09-17 role, control and outcome-focus follow-through

The first release still presented Health, Architecture and Strategy as peer
work areas, allowed workflow-specific playbook focus to leak into technical
and architecture review, and had no direct one-off reviewer controls. The role
contracts and Pulse workspace now make the intended hierarchy explicit:

- Plan Drift is the exclusive compatibility prerequisite after plan changes.
  Technical, Architecture and Strategy wait for a clean current plan instead
  of reviewing stale structure or evidence.
- Technical owns concrete correctness failures and regressions. Architecture
  owns evidence-backed improvements to the technical construction of an
  otherwise working plan. Strategy owns goals, outcomes, measurement,
  assumptions, useful alternatives and self-improvement.
- Installed playbook custom focus is Strategy-only. Technical and Architecture
  retain their canonical platform-owned scopes rather than receiving arbitrary
  workflow-specific focus lists.
- The Pulse UI leads with goals, metrics and Strategy. Technical, Architecture
  and Plan Drift are grouped as platform health and stability. Strategy opens
  by default without filtering the complete issue backlog.
- Goal progress keeps the primary outcomes scannable: supporting/secondary
  metric cards are collapsed by default and appear only when the user opens
  their linked primary metric. Opening one primary does not reveal another
  primary's supporting metrics; unassigned supporting metrics produce only a
  compact setup notice until they are linked.
- The first review area now keeps Strategic Review and its proposal lifecycle
  together. `strategy_experiment` interventions appear as **Strategic
  proposals** directly beneath the Strategy card, before platform maintenance
  or the general decision queue. The former mixed **Improvements** block is now
  a lower **Platform improvements** section limited to Technical fix bundles
  and Architecture improvements, so those records no longer displace or
  duplicate Strategy's proposals.
- Technical, Architecture and Strategy each expose a durable **Run
  automatically** toggle and an independent **Run now** action. A disabled
  automatic reviewer can still run once by hand. Plan Drift remains mandatory,
  cannot be disabled, and has its own manual drift-check action. A due or
  unavailable Plan Drift status disables the three downstream manual actions.
- Manual actions reuse the canonical guided review contracts in the workflow's
  interactive Builder chat; they do not invent a second execution path.

Scheduling now has an explicit research-horizon asymmetry without adding new
cron jobs or a universal fixed interval. Architecture normally waits across
several comparable producing runs for stable structural evidence. Strategy is
reconsidered at the next meaningful goal, outcome, feedback, experiment,
decision or measurement checkpoint, so it normally receives the shorter
research horizon. Material evidence and reached checkpoints can still override
either wait.

Strategy also has a symptom-loop guard. A recurring operational label such as
`booking-heavy` is Technical context, not a sufficient strategic agenda. The
reviewer deduplicates or hands off the concrete defect, assumes it is fixed,
then asks what would still limit the primary goal. Every completed Strategic
Review must contain a goal-derived conclusion (including an honest no-change
conclusion); a technical handoff alone is not completion.

### Local verification

- 73 Pulse-focused frontend tests pass.
- The focused Goal Progress, metric-grouping and Pulse workspace suites pass,
  including primary-specific supporting-metric disclosure.
- Frontend TypeScript build passes.
- Complete `cmd/server/guidance` Go test suite passes, including regression
  coverage for distinct Architecture/Strategy horizons and the recurring
  symptom guard.
- `git diff --check` passes.

Deployment and a live RTS Pulse acceptance pass remain open. Live acceptance
must confirm that Architecture is selected less often than Strategy over
several comparable schedules, Strategy produces a goal-derived conclusion,
and an existing `booking-heavy` Technical issue is not recreated or used as the
entire Strategic Review.

## Register notes

[PLAT-305](plat-305.md) implements independent
review roles, sequential scheduling with protected research dates, authorized
research access, linked decision/application/outcome tracking, and the revised
Pulse workspace. Goals, metrics and Strategy are primary; Technical,
Architecture and Plan Drift are platform-health controls. Technical,
Architecture and Strategy have persistent automatic toggles plus one-off manual
run actions; Plan Drift stays mandatory and blocks downstream runs while due.
[PLAT-303](plat-303.md) now gives Architecture
the longer evidence horizon and Strategy the shorter meaningful-outcome horizon.
Strategy is explicitly prevented from treating recurring operational labels such
as `booking-heavy` as its whole agenda. These changes are locally tested on
2026-09-17; deployment and live workflow acceptance are pending. Immutable
partial-run evidence (PLAT-047/089) and comprehensive builder prevention
(PLAT-257/298) remain separate unfinished foundation work.
