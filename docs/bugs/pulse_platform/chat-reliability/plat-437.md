# PLAT-437 — Builder chats were treated as "provider changed" on every message (regression of PLAT-425)

Status: fixed on main; deployment pending. The broken behaviour **is deployed** (Excellence, Confida, RTS run PLAT-425 since 2026-10-04 ~13:00 IST). Found 2026-10-04 on the local app.

## What was wrong

PLAT-425 made a send after a provider switch wait for the running turn (queue) and then relaunch the CLI on the new provider. It compared the
retained CLI's provider with the provider named by the request. For a Builder (workflow_phase) chat whose workflow has its own LLM, handleQuery keeps
the **manifest's** provider and ignores the request's (the Models-page default), so the two differed on every message:
- a running chat showed "Queued behind the active conversation turn" instead of live steering (local Codex chat, request said claude-code), and
- between turns the retained CLI was interrupted and relaunched on every send (the chat area visibly reloaded).
Log signature: `[CHAT_HISTORY] Provider changed for session ...: the retained CLI is not <provider the request named>` repeated per message.

## Fix

`workflowManifestDecidesProvider`: when the chat is a Builder chat whose manifest names an LLM and the request's LLM config does not come from a product
profile, the request's provider is not compared; a change of the manifest's own provider stays with the workflow retained-delivery policy.
Product chats (Crew, Code) and workflows without their own LLM keep the PLAT-425 behaviour. Test `TestWorkflowManifestDecidesProviderOfABuilderChat`.

## Left

- Check the symptom is gone on Excellence/Confida/RTS after deploy (workflow Builder chat: send mid-answer, it steers; no repeated "Provider changed" lines).
- Make the queue label say why it waits when a provider change really queues (offered to the owner, not built).
- Why the request names a provider that is never used (the frontend sends the Models-page default for manifest-driven chats) is untouched.
