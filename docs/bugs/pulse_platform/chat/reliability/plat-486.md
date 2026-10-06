[← chat / reliability](index.md)

# PLAT-486 — Muse's "AgentWorks note: platform bridge mounted" shows in chat messages

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | - |
| Product | chat |
| Area | reliability |
| Summary | fixed on main: the model-only "AgentWorks note: platform bridge mounted" text is stripped from Muse messages read back into the chat. |

| Coordination | Value |
|---|---|
| State | fixed on main (provider `c85e709`, pinned in the builder); needs a restart |
| Date | 2026-10-05 |
| Owner | coding-agent-bridge |

## Source

The owner saw "[AgentWorks note: the platform bridge is mounted for this run ...]" at the start of automatic
notification messages in the chat area, which broke the layout.

## Cause

On a resumed Muse conversation with the bridge mounted, the adapter puts that note in front of the user's
message so the model ignores older "no bridge" statements. Messages read back from the Muse transcript (the
two readers in `musecli_transcript.go`) returned the text with the note, so the chat showed it.

## Done

- The transcript readers strip the note (`museStripResumeNote`); the model still receives it. Unit test added.

## Left

- Codex (structured) and Cursor wrap the prompt as `[System Instructions] ... [User Message]`. No readback
  path returns that as a human message that I could find; not confirmed live. If it shows up in a chat,
  strip it the same way.

## Register notes

[PLAT-486](plat-486.md), fixed on main: the model-only "AgentWorks note: platform
bridge mounted" text is stripped from Muse messages read back into the chat. Left: Codex/Cursor wrappers unconfirmed.
