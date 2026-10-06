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
- Checked by rendering the real profile prompt; fix 1 is live on RTS (the chat went straight to the repository card), the backup flow change is not deployed.

## Left

- Deploy to RTS, then ask the Brain chat to set up backup and confirm it goes straight to the details.
- Not a bug: the Brain tab's empty-looking tree on RTS was `RTS/Latency` collapsed; files load when a folder is expanded (gateway log shows the deeper requests succeed).
