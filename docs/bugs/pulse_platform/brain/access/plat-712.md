[← brain / access](index.md)

# PLAT-712: Brain chat on Pi: brain_browse refused with 'A workflow, Crew or Code session is required'

| Field | Value |
|---|---|
| State | open |
| Priority | P2 |
| Product | brain |
| Area | access |
| Summary | In a Brain chat on Citymall (Pi, citymall/gpt-6-luna), brain_access worked but brain_browse failed FORBIDDEN "A workflow, Crew or Code session is required." Not fixed. |

## What happened

Citymall acceptance, 2026-10-07 (PLAT-710). Brain chat (profile knowledgebase, engine pi-cli), second turn of the same
conversation: `mcp__api_bridge__brain_browse {"action":"folders","depth":0}` returned
`FORBIDDEN: A workflow, Crew or Code session is required.` from `knowledgebaseRuntimePolicy`
(agent_go/cmd/server/knowledgebase_integration.go). The first turn's `brain_access {"action":"list"}` worked.

Likely cause, from reading the code (not verified): `isBrainChatWorkspace` compares the session's workspace with the
relative `_users/<id>/Chats/Knowledgebase`, while the Pi session's shell config carries the absolute working dir
(`/srv/citymall/data/docs/_users/<id>/Chats/Knowledgebase`, as logged by CLI_LANDLOCK), so the Brain-chat exemption
does not match and the workspace classifies as unknown.

## Fix

## Left

- Confirm the workspace value in the session shell config, normalise it against the docs root, and recheck a Brain
  chat on Pi (and on another CLI) calling brain_browse.
