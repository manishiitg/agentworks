---
name: agentworks-add-new-thing
description: "Use when the person links or asks you to look after a new Goal, Crew or Relay: notes and the daily check."
---

# Adding a new Goal, Crew or Relay to what you look after

You work in your main chat; you never open tabs (only the person makes tabs).

**Goals the person runs are set up by the platform.** Each one is linked to you automatically and gets a notes file `notes/goal-<name>.md`; your one daily check in the main chat covers every linked Goal, and a failed or missed scheduled run wakes your main chat at once. Do not create these yourself. When a new Goal appears, fill in its notes (below) and add it to `notes/overview.md`.

**Crews and Relays are linked only when the person asks.** Linking a Crew gives you read and write on its files: look, change nothing there unless they say so.

1. **Find it.** Use `workspace` action `references_list` to list what is linked; note the exact `workspace_path` and id. For a Crew or Relay the person asked you to look after, use `workspace` action `reference_attach` only on their request.
2. **Write its notes:** `notes/goal-<short-name>.md`, `notes/crew-<short-name>.md` or `notes/relay-<short-name>.md`, in the same shape as the others: a first line saying what it looks after, `Mode: watch`, Focus, Targets, Rules, Latest lessons (at most five).
   - Goal: copy the purpose and success criteria from its soul file; add numeric targets you can verify, or say 'measurement setup needed'.
   - Crew: summarise its instructions in two lines and ask the person once what a good week looks like.
   - Relay: its function, its callers, and what a healthy call looks like.
3. **Record it.** Add it to the list in `notes/overview.md` (thing, notes file, mode). The daily check reads that list, so a Crew or Relay listed there is checked every day with `agentworks-inspect-crew` or `agentworks-inspect-relay`; do not add a separate schedule for it. Tell the person in one line what you set up.

If the person asks you to stop looking after a Goal, detach it (`workspace` action `reference_detach`): the platform then archives its notes, and it stays unwatched until they link it again. For a Crew or Relay, remove it from the list in `notes/overview.md` and move its notes to `notes/archive/`.
