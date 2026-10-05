# PLAT-500: Hide automatic incoming Gmail from local UI

State: fixed on main; frontend reload/build and RTS release deployment pending.
Priority: P2. Owner-requested follow-up of [PLAT-499](plat-499.md).

## Decision and scope

The owner wants local Google connections to stay simple, without incoming-mail
Cloud provisioning or tunnel setup in the UI. Hide the shared Incoming email
panel locally in both Integrations and Triggers for workflows, Crew and Code.
Keep account connections, permission management and ordinary Gmail read/send.
This is a UI choice; it does not disable the backend capability or delete saved
receiving configuration, watches, rules or Google Cloud resources.

## Change

Visibility follows the connected backend's existing `/api/capabilities`
`local_mode` flag, not the browser URL. A local backend behind a tunnel stays
hidden; a remote backend opened from the desktop stays eligible. Incoming UI
is hidden until capabilities identify a server deployment. The shared panel
never mounts its route loader or polling locally and clears its trigger counts
when hidden.

Google apps Ask AI prompts in workflow, Crew and Code panel headers and account
sections offer account/service access locally without incoming infrastructure
instructions. Server prompts retain the existing reviewed provisioning flow.
The visibility subscription updates when backend capabilities change.

## Verification

One focused UI regression pins the local/server boundary: local mode renders
no incoming panel and makes no route fetch; Google apps prompts still offer
connections without setup_gmail_inbound; changing to server capabilities mounts
the panel and restores provisioning guidance. Existing Incoming email, Gmail
permission/account and integration prompt checks plus TypeScript compilation
verify the shared surfaces. All 46 selected UI checks and TypeScript compilation pass.

A real-browser preview using the shared components confirms local mode retains
the account identity and Change access control, has no Incoming email section
and makes zero route requests. Its Ask AI message contains account-access
guidance only. Switching the connected backend capability to server mode
restores Incoming email, its Ask AI action and the setup checklist.

## Remaining

Reload/rebuild the local frontend to pick up this UI change. Include it with
PLAT-499 in the next RTS release; Cloud consent and real delivery verification
remain tracked by PLAT-483/PLAT-499.
