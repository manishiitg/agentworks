# PLAT-512: Compact Google accounts and clarify notification enablement

State: fixed on main; RTS deployment pending.
Priority: P2. Owner-requested UI follow-up to PLAT-500.

## Problem and decision

Multiple connected accounts repeat six large app tiles, most with no access,
plus long OAuth app identifiers. Account identity and useful permissions become
hard to scan. A Gmail-wide On/Off badge and Enable Gmail toggle also imply that
a signed-in mailbox needs a second enable step, although that toggle controls
the workflow notification channel.

## Change

The shared GoogleAccountList starts collapsed. Each row shows account identity,
connection status, default marker and an icon summary of selected apps. Gmail
still shows Notifications only when agent read/write access is off. Inactive
apps and the OAuth app identifier appear inside Permissions, in a table that
compares AgentWorks settings with Google grants. Only one account's comparison
opens at a time; Change access closes the comparison and opens the existing
prefilled editor. Missing-grant and disabled-reading warnings remain visible
while collapsed. Narrow panes keep account actions together and wrap identity.

Workflow/Crew/Code/Relay shared account consumers receive the same presentation.
Delivery settings now say Workflow email notifications, with copy explaining
automatic activation and the existing deliberate-disable exception. Remove the
Gmail-wide On/Off badge. No account, scope, authorization, notification enablement
or incoming trigger behavior changes. Local incoming UI remains hidden.

## Verification

23 existing/updated account, access editor and Gmail management checks pass.
One focused account regression pins initial collapse, single-account expansion,
collapse and prefilled Change access. The shared management checks retain the
owner/admin removal restrictions and pin the clarified notification label.
TypeScript compilation passes.

Browser verification uses the real shared account list/editor with four sample
accounts: collapsed summaries, grant comparison, switching the expanded account,
and changing the selected account's editor retain the correct identity and
access values. At 390px, the page has no horizontal overflow and the permission
table remains readable. Screenshots inspected locally in
artifacts/google-accounts-compact outside the source worktree. This preview does
not change real Google accounts or test live OAuth authorization.

## Remaining

Reload the local frontend; include the shared UI in the next RTS release.
