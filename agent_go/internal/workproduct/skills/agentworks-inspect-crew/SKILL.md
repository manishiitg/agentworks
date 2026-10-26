---
name: agentworks-inspect-crew
description: "Use to inspect a Crew in depth: its instructions, files, tabs, schedules, functions and connections."
---

# How to inspect a Crew in depth

Start from the Crew's notes (`notes/crew-<name>.md`). A Crew has no success criteria like a Goal, so what it is for comes from its instructions and from what the person told you.

## Tools and skills
- The Crew's identity and path: `workspace` action `references_list`. Read its instructions and other files only where their own read grants allow. Use the linked metadata and authorized files until the platform adds a Crew inspection tool. Skills: `crew-builder`, `panel-data`, `panel-settings`, `panel-dashboard`.
- Its chats and tabs: `sessions` action `list` where authorized (never open a tab); earlier conversations through `panel-runs` (chat history). To message it or a tab: `messages` action `list_agents`, then `messages` action `send` with the right `chat_id`. A stored message is not finished work; read the reply with `messages` action `read`.
- Its schedules and watchers: `schedules` action `list` and `watchers` action `list` for this Crew. Skills: `panel-automations`, `schedules`, `watchers`.
- Its connections and secrets: skill `panel-access` (names only, never values).
- A Crew linked to you is read and write: look, but change nothing unless the person has said you may act on it.

## What to check
1. **Instructions:** do they still say what the person wants; anything stale, contradictory, or too long to follow.
2. **Recent work:** did it do what was asked in its latest chats; did it get stuck; did the person correct it on the same thing more than once (that is the strongest signal something is wrong in the instructions).
3. **Schedules and watchers:** running, failing, duplicated, firing in the wrong tab.
4. **Functions and connections:** broken, unused, or missing a secret (name only).
5. **Output vs purpose:** is what it produces what the person actually uses.

## Finish
One plain line on the cause, what you verified and what you could not, and what you did or propose. Save anything lasting to the Crew's notes file.
