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

## Left (not fixed here)

- The session died while a second message arrived during Muse's startup after a definition-change relaunch ("Message not sent — another run is still starting"); the relaunch itself was from the deploy changing the Code definition.
- Muse still printed `local session messaging unavailable: registry_io ... Permission denied` in this session despite the private XDG_RUNTIME_DIR (PLAT-417); recheck after this deploy.
