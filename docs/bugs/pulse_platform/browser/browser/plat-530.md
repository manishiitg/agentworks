# PLAT-530 — Workflow extension rollout and immediate current-tab sharing

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | browser |
| Area | browser |
| Summary | fixed on main, not deployed. |

State: fixed on main, not deployed. Date: 2026-10-05. Priority: P2. Requested by owner.

## Behavior

- Workflow Browser settings expose the existing extension setup. Account tokens
  stay stable across Code, Crew and workflows, with separate scope/capability,
  targets, groups and controllers for each account/workspace binding.
- Pairing/connection/heartbeat require AgentWorks product access and workflow
  write access at an existing canonical workflow root. Readers, unknown accounts,
  wrong profiles, subfolders and Relay manifests are rejected. Another owner can
  pair their own browser without receiving the first owner's binding.
- The authenticated tool-context binder supplies the parent authority session
  as WorkflowSessionIDKey. Registered workflow child/group sessions retain that
  controller, while their ChatSessionIDKey and filesystem grants remain local.
  Sequential steps with agent_browser enabled may reuse that run's tabs. Another
  run/chat cannot take over a live controller; reconnect to change controllers.
  The browser must remain online for scheduled/background steps; stop fails closed.
- Active workflow chat notices follow its durable preset identity through the
  global queue, with response revalidation and no notices into execution diagnostics,
  view-only, schedule or bot observer tabs. Workflow switching drops stale responses.
- Human Connect browser immediately shows connected and shares the current HTTP(S)
  page or about:blank. Protected pages, Chrome-refused pages and tabs already shared
  with another workspace leave the connection usable with zero tabs. Automatic
  account-project attachment never adopts the user's active page. Agents can
  create their first tab without manual sharing. Explicit Share remains available.
- Extension 0.3.1 adds Workflow labels and the current-tab sharing explanation.
  Account Reset now explains that workflow connections are also revoked.

## Verification

- Real Chrome 153.0.8010.12 and Edge 154.0.4258.53 checks pass through the
  guarded workspace shell, native agent-browser, relay and unpacked extension.
  Step one creates/fills a workflow tab; step two clicks/reads it using the same
  parent controller. Workflow groups/targets remain separate from Code; existing
  Code/Crew isolation and background/focus checks still pass. Human Connect
  reports connected, shares the current website and permits a snapshot without Share.
- Management/socket/heartbeat handler checks pass for shared account tokens,
  owner-only personal bindings, reader/product/profile/subfolder/Relay refusal
  and access revocation. Registered-child context checks prove a forged parent
  controller is replaced by the authenticated authority session.
- Browser-rendered settings and real global queue checks pass for workflow
  connected states with zero tabs, workflow preset-bound notices, Code/Crew
  regression coverage and dark/light/narrow layouts. Four focused notification
  checks and the production frontend build pass; package matches extension source.
- No actual model or scheduled job is launched by the browser tool fixture.

## Remaining

Rebuild/deploy the platform and reload the unpacked extension 0.3.1. Relay rollout
remains outside this request. Existing direct-CDP security review tickets remain open.

## Register notes

[PLAT-530](plat-530.md), P2, fixed on main, not deployed. Account-private workflow pairing, step-parent controller identity, workflow UI/notices and human Connect sharing the current website automatically.
