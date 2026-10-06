[← crew / chat](index.md)

# PLAT-614: Distinguish shared provider account identity from the Crew user and Gmail mailbox

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | crew |
| Area | chat |
| Summary | A Crew treated its Claude account email as the app user and suggested it as a Gmail mailbox despite an empty connection list. |

## What happened

Ashutosh's `browser-test` Crew suggested `patelvaibhav122003@gmail.com`.
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

## Left

Deploy and verify generated instructions on Excellence. Old replies remain
historical; a restarted/resumed session must receive the corrected instructions.
