[← crew / chat](index.md)

# PLAT-616: Distinguish shared provider account identity from the Crew user and Gmail mailbox

| Field | Value |
|---|---|
| State | deployed |
| Priority | P2 |
| Product | crew |
| Area | chat |
| Summary | A Crew treated its Claude account email as the app user and suggested it as a Gmail mailbox despite an empty connection list. |

## What happened

Ashutosh's `browser-test` Crew suggested `<a member's email>`.
Its recorded `list_gmail_connections` result was `connections: []`; no Gmail
mailbox read was recorded. Vaibhav's workflow used its own private gws store.
The Crew's Claude account cache named that address, while the generated product
prompt supplied neither Ashutosh's identity nor a distinction between the
provider login and app user. The underlying contaminated provider login is
tracked separately in [PLAT-615](../../coding-agents/accounts/plat-615.md).

## Fix

Code/Crew and other direct product chats now receive the signed-in AgentWorks
identity through the prompt-section registry. Workflow Builder/Run retains its
existing identity section with the same clarified wording. Provider login and
billing identity establish neither the app user nor mailbox access. The Gmail
skills require scoped connection evidence and forbid guessing a mailbox from
provider metadata or an app login email.

## Validation

Real product prompt composition fixtures cover Code, Crew Builder and Crew Run
and assert the app identity and provider distinction. Existing workflow identity
regression covers the shared wording. These focused checks pass on the Mac.

## Deployment

Deployed to Excellence in `agents-bd97744f-20261006143646`. Product prompt
composition and authenticated-user regressions also passed on Excellence's
Linux host. The release's saved source confirms the real request path supplies
the signed-in user and the corrected identity section. The affected retained
Claude session was closed as part of PLAT-615; its next authorized turn receives
new instructions. No artificial message was sent to another person's chat.
Old replies and historical prompt snapshots are not rewritten.
