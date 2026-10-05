# PLAT-545 — Workflow steps fall back to local CDP instead of the selected extension

State: fixed on main, not deployed. Date: 2026-10-05. Priority: P2. Reported by owner.

## Evidence and cause

The Upwork workflow builder used its paired extension, while its
`search-find-and-shortlist` message-sequence step logged `mode=cdp`,
`shared-cdp-9222` and `http://localhost:9222` at 23:35 IST in the local server log.
The workflow/group browser namespace was already bound to the builder.

Builder tools receive the authenticated query binding through LLMAgentWrapper.
Workflow steps assemble their direct tools separately. Their fresh scripted/CLI
HTTP bridge contexts carried a child session but no account identity, so the
account-private extension lookup returned nothing and the executor fell through
to configured CDP. Dedicated step sessions also lacked the HTTP-parent registry
relationship required by that authenticated binding; groups alone had it.

## Fix

- Bind browser executors to the authenticated query owner/controller when
  assembling both workshop and full-workflow tools. Reuse the existing identity,
  ownership, permission and revocation checks instead of inventing a local user.
- Register dedicated execution, message-sequence and todo-tool sessions under
  the parent HTTP run when binding their shared browser. Namespace or a similar
  session name alone never authorizes borrowing another run's tools.
- Preserve the child's session and narrow folder grants for screenshots and
  other browser artifacts. The extension remains account/workspace scoped.
  A selected offline extension stays extension mode and refuses actions rather
  than falling back to a local browser.

## Verification

- One regression check uses the real HTTP executor, per-step tool registry and
  live relay WebSocket. Execution and message-sequence requests without user
  metadata report extension mode and keep their own screenshot grants, share
  the parent's controller, reject conflicting owners/unregistered children, and
  stop on socket loss. No model or user browser was invoked by this check.
- The existing browser-isolation check now exercises actual child binding and
  parent registry propagation; identity-binding, browser runtime and affected
  message-sequence lifecycle checks pass.
- Real unpacked Chrome extension qualification passes: workflow step one
  creates/fills a tab, step two reads/controls it, Code/Crew targets remain
  isolated, and disconnect/reconnect/restart checks pass. This ran against
  isolated fixture pages, not the user's Upwork account.

## Remaining

Restart/deploy the backend and start fresh workflow steps so their registered
executors receive the corrected binding. Existing running steps keep their
old tool definitions until stopped/recreated.
