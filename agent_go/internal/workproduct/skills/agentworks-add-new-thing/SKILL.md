---
name: agentworks-add-new-thing
description: "Use when the person links or asks you to look after a new Goal, Crew or Relay: tab, notes, daily check."
---

# Adding a new Goal, Crew or Relay to what you look after

**Goals the person runs are set up by the platform.** Each one is linked to you automatically and gets its own tab (named after the Goal), a notes file `notes/tab-<name>.md`, a daily check an hour after its first daily run when it has timers, and a wake-up that brings a failed scheduled run to its tab at once. Do not create these yourself. When a new Goal appears, fill in its notes (below) and add it to `notes/overview.md`.

**Crews and Relays are linked only when the person asks.** Linking a Crew gives you read and write on its files: look, change nothing there unless they say so.

1. **Find it.** Use `workspace` action `references_list` to list what is linked; note the exact `workspace_path` and id. For a Crew or Relay the person asked you to look after, use `workspace` action `reference_attach` only on their request.
2. **Open its tab** (Crew or Relay only). Use `sessions` action `open` with the thing's name; note the returned chat id.
3. **Write its notes:** `notes/tab-<short-name>.md`, in the same shape as the others: `Mode: watch`, Focus, Targets, Rules specific to this tab, Latest lessons.
   - Goal: copy the purpose and success criteria from its soul file; add numeric targets you can verify, or say 'measurement setup needed'.
   - Crew: summarise its instructions in two lines and ask the person once what a good week looks like.
   - Relay: its function, its callers, and what a healthy call looks like.
4. **Give it a daily check** (Crew or Relay only). Use `schedules` action `create` for this Crew with `chat_id` set to the tab, one self-contained message (read the tab notes, follow `agentworks-inspect-crew` or `agentworks-inspect-relay`, compare with targets, find the cause of any miss, save what you learned, report in the tab), at a time after the thing's own work, never at the same minute as another check.
5. **Record it.** Add the tab to the table in `notes/overview.md` (tab, chat id, thing, mode). Tell the person in one line what you set up and when the first check runs.

If the person asks you to stop looking after a Goal, detach it (`workspace` action `reference_detach`): the platform then removes its check and tab and archives its notes, and it stays unwatched until they link it again. For a Crew or Relay, pause its daily check, keep the tab and notes as history, and remove its row from the table.
