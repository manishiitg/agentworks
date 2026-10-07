[← code / shell](index.md)

# PLAT-645: Backgrounded shell commands still block the chat's next commands

| Field | Value |
|---|---|
| State | open |
| Priority | P2 |
| Product | code |
| Area | shell |
| Summary | A shell command the bridge moved to the background after 120 s still holds the chat's shell; later commands wait silently and the agent concludes the shell is stuck |

## What happened

## Fix

## Left

## What happened

RTS, 2026-10-07, SDE Private Code, side chat `task2`: the agent started a PR 189 review and, in the same command, polled GitHub every 20 s up to 25 times (about 10 minutes). After 120 s the MCP bridge reported the command "moved to the background as task …; keeps running; you can keep working". The agent then ran three more commands (PR 190 lookups, then `echo alive`); none started, because the chat's shell runs one command at a time and the backgrounded one still held it. The agent concluded the shell itself was stuck. The loop finished on its own; nothing on RTS was broken.

## To decide and fix

- Either a backgrounded command stops holding the chat's shell (later commands run alongside it), or a waiting command is told plainly: "the shell is still running <command summary>, started <n> min ago; wait for its notification or stop it".
- Agent guidance: poll in a background task or with a short loop, not a 10-minute loop in a foreground command.
