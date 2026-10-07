[← coding-agents / muse](index.md)

# PLAT-676: Muse hooks fail on slots: node Permission denied

| Field | Value |
|---|---|
| State | in progress |
| Priority | P1 |
| Product | coding-agents |
| Area | muse |
| Summary | On slot hosts every Muse PreToolUse hook failed: the hook ran the service account's nvm node, which slot accounts cannot execute |

## What happened

## Fix

## Left

## Report

#agent_works, 2026-10-07 14:49-14:52 (Vishwas, Excellence, Code on Muse): while the agent was working he added the Postman MCP; the message was "Not delivered" and the run ended with `muse tmux session ... died before run completion`. The owner saw `Hook failed · PreToolUse · /bin/bash: .../srv/agents/home/.nvm/versions/node/v24.21.0/bin/node: Permission denied` in the same Muse.

## Cause (hook)

Muse's PreToolUse hooks (the tool allowlist and the shell redirect) were written as `node '<hook>'`. On a slot host Muse runs as the person's Linux account; `node` resolved through PATH to the service account's nvm install (`/srv/agents/home/.nvm`, mode 770), so every hook failed.

## Fix

multi-llm-provider-go 764d01e: the hook names an absolute node that any account can execute (PATH node if its file and folders are o+x, else /usr/local/bin/node, /usr/bin/node). Excellence has /usr/bin/node.

## Cause (session died on the second message)

Excellence log, session product-4390f9bb, 11:16:24-11:16:41 CEST: the first message switched the chat from Codex to Muse and relaunched it; 17 s later, while that Muse turn was running a tool, a second message arrived and the Muse tmux session died before `handleQuery` even logged the request. The kill came from `prepareProductConversationTurn` (`agent_profile_routes.go`): when `bindRuntimeConfiguration` reports a runtime change it closed the coding CLI at once (`closeCodingCLIAndReleaseTurnMarkers` + `Session.Close`), whatever was running. The second message counted as a change because of the account default: the first turn after a provider switch binds no account, and the next message pins the server account (`global:muse-cli`, ea08805b2), which differs from the stored empty one. So in any chat, a message sent while the first turn after a provider switch, or a new chat's first turn, was still running could kill that turn. "Message not sent — another run is still starting" is Muse's routine notice on the first submit of a start (logged as MUSE_SUBMIT_NOTICE in nearly every session); the adapter already ignores it and it played no part.

## Fix (second message)

A runtime change found while the chat's turn is running no longer closes anything. The message is marked `restart_coding_cli` (with live delivery off), `handleQuery` queues it behind the running turn (as a runtime change, so an idle live turn is ended the usual way), and the CLI is closed and relaunched only when that message's own turn starts (`retireProductCodingCLI`). With no turn running the restart happens at once, as before. Test: `TestProductRuntimeChangeDuringRunningTurnDefersTheRestart`.

## Left

- The account default still flips a chat's binding from "none" to the server account on its second message, which restarts the CLI once between turns. That now costs only a relaunch, but for a person with their own Muse account the second turn moves to the server account. It needs an owner decision on which account a new chat binds.
- Muse still printed `local session messaging unavailable: registry_io ... Permission denied` in this session despite the private XDG_RUNTIME_DIR (PLAT-417). By the code, the confined interactive launch puts XDG_RUNTIME_DIR into the launch script (`museAccountLaunch` → `museRuntimeEnv`). The warning only shows in the Muse pane, never in agent.log, so it was not rechecked live; look at a slot Muse pane after the next deploy.
