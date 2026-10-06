# PLAT-437 — Builder chats were treated as "provider changed" on every message (regression of PLAT-425)

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | - |
| Product | chat |
| Area | reliability |
| Summary | regression of PLAT-425 (deployed), fixed on main; deploy pending. |

Status: fixed on main; deployment pending. The broken behaviour **is deployed** (Excellence, Confida, RTS run PLAT-425 since 2026-10-04 ~13:00 IST). Found 2026-10-04 on the local app.

## What was wrong

PLAT-425 made a send after a provider switch wait for the running turn (queue) and then relaunch the CLI on the new provider. It compared the
retained CLI's provider with the provider named by the request. For a Builder (workflow_phase) chat whose workflow has its own LLM, handleQuery keeps
the **manifest's** provider and ignores the request's (the Models-page default), so the two differed on every message:
- a running chat showed "Queued behind the active conversation turn" instead of live steering (local Codex chat, request said claude-code), and
- between turns the retained CLI was interrupted and relaunched on every send (the chat area visibly reloaded).
Log signature: `[CHAT_HISTORY] Provider changed for session ...: the retained CLI is not <provider the request named>` repeated per message.

## Fix

`effectiveProviderOf` returns the provider the request will actually run on, in the order handleQuery applies: a Builder chat's manifest LLM (unless the
request's LLM config comes from a product profile), then the locked server's answer (`resolveLockedLLM`: a locked server honours the request only for a
product profile or a published model), then the request's own choice. The PLAT-425 check compares the retained CLI with that, so it only fires when the
provider that runs really changed. This covers every product on a locked server (RTS) and Builder chats everywhere, not only Builder.
Test `TestEffectiveProviderIsWhatActuallyRuns` (manifest, product profile, plain chat, no workflow LLM, locked server).

## Left

- Check the symptom is gone on Excellence/Confida/RTS after deploy (workflow Builder chat: send mid-answer, it steers; no repeated "Provider changed" lines).
- Make the queue label say why it waits when a provider change really queues (offered to the owner, not built).
- Why the request names a provider that is never used (the frontend sends the Models-page default for manifest-driven chats) is untouched.

## Register notes

[PLAT-437](plat-437.md), regression of PLAT-425 (deployed), fixed on main; deploy pending. A workflow whose manifest
names its own LLM, or a locked server (RTS), ignores the request's provider, but the check compared it with the retained CLI: every send queued behind a running turn and relaunched the CLI.
