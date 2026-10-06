[← brain / chat](index.md)

# PLAT-566: Brain chat is told who is signed in and whether they are an administrator

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P3 |
| Product | brain |
| Area | chat |
| Summary | The Brain chat did not know the caller's role and asked an administrator whether they were one |

## What happened

## Fix

## Left

## What happened

RTS, 2026-10-06: asked to set up Git backup, the Brain chat answered "I can't tell from here" when the owner (an administrator) asked whether they were one, and wanted their GitHub username before doing anything. The chat prompt was static text ("ask an administrator for ...") and carried nothing about the caller. The server decides every request (`configure_backup` needs an unrestricted administrator), so this was only the model not knowing.

## Fix

- `knowledgebaseCallerLine` (cmd/server/knowledgebase_runtime.go) states the caller's role; registered as the Brain profile's prompt variable `CALLER` (server.go) and used in `access-builder.md`.
- The backup instruction now asks for the repository URL, the GitHub username and the branch, not "ask an administrator".
- Owner, 2026-10-06 ("why is the agent so limited"): a request that says to set up the backup and push now covers status, commit and push after the setup card is approved, instead of three more asks. Configure-only requests still stop after setup. The approval card stays the consent step; discard, branch changes and anything not asked for are unchanged.
- Owner, 2026-10-06 ("it should all be agentic like other products", "token will store in secrets"): the Brain chat no longer shows an approval card for binding or unbinding a project, setting its Brain access mode, or backup setup; they run when asked and the chat reports what changed. The card stays for grant, revoke and service accounts (note text is untrusted). A Git token never passes through the chat: setup takes `pat_secret`, the name of a platform (global) secret the administrator adds under Secrets, and Brain reads it when it runs Git (`BACKUP_SECRET_MISSING` if it is gone); a raw token sent through the chat is refused. The old `pat` argument and the card's token field still work for the app and MCP paths.
- Owner, 2026-10-06 ("the agent can add the token", "same exact way like it happens in all builder chats"): the Brain chat now has `list_secrets` and, for administrators, `manage_global_secret`, like builder chats. Given a token, it saves it as a Vault secret (for example `BRAIN_GITHUB_PAT`) and sets up backup with `pat_secret`; the token still never goes into `configure_backup`. As in builder chats, a token typed into the chat passes through the model.
- Owner, 2026-10-06 ("i dont see the secrets options ... i manually add secrets and chat has access, just like other products"): the Brain tab has a Secrets view (the same platform-secret panel Vault uses). A token added there is what the chat finds with `list_secrets` and what backup names with `pat_secret`.
- Tests: two card-flow tests replaced by `TestKnowledgebaseChatBackupSetupIsDirectAndTokenIsASecret`.
- Checked by rendering the real profile prompt; fix 1 is live on RTS (the chat went straight to the repository card), the backup flow change is not deployed.

## Left

- Deploy to RTS, then ask the Brain chat to set up backup and confirm it goes straight to the details.
- Not a bug: the Brain tab's empty-looking tree on RTS was `RTS/Latency` collapsed; files load when a folder is expanded (gateway log shows the deeper requests succeed).
