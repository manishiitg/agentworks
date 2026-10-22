---
name: panel-google-apps
description: Google apps panel in Goals and Crews. Connect, select, change access to or remove a saved Google account.
metadata:
  panel-tools:
    work:
    - manage_google_account
    - list_gmail_connections
    - update_gmail_connection_grants
    agentworks:
    - manage_google_account
    - list_gmail_connections
    - update_gmail_connection_grants
    relays:
    - manage_google_account
    - list_gmail_connections
    - update_gmail_connection_grants
  external-tools:
  - workflow_google_account
---

# Google apps

Use manage_google_account action=list_saved first. attach/detach is Use here/Remove here; connect/reconnect returns the real sign-in link. Only the owner completes Google sign-in. Respect observed read/write grants; do not add another login when one already exists. For older owned private accounts, the existing attach operation records the personal selection before set_inbox_account; do not edit flags in files. Use list_gmail_connections to verify. google_workspace_cli performs authorized service work, not account setup.

For personal-account selection, old private-account adoption and grants, Read [the detailed procedure](references/connected-accounts.md).

For this panel’s full setup and operational contract, Read [the detailed procedure](references/operations.md).
