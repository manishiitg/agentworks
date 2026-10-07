[← relays / frontend-chat](index.md)

# PLAT-679: External Relay builder chat cannot find draft test tools

| Field | Value |
|---|---|
| State | open |
| Priority | P2 |
| Product | relays |
| Area | frontend-chat |
| Summary | Relay guidance asks externally submitted Builder turns to test, but their managed editing tool policy omits draft test tools. |

## What happened

On Dominion, MCP `builder_chat` updated `Workflow/relaygraphsmoketest/relay.py`
to add `response_due_hours`. Operation `b54d829f-b2f0-4803-94f4-a9cdf0d2c854`
completed in the owner's existing main chat. The Builder reported that
`test_relay` and `get_relay_run` were unavailable and could not verify the change.
The caller's OAuth grant included `builder:chat`, `relays:write` and
`runs:execute`, and the Relay role was Owner.

The Relay product manifest and `/test` command tell Builder to use these tools,
whereas the external Builder turn intentionally uses a managed editing policy
and disables shell/connected account tools. Review the guidance and that policy
together before deciding whether scoped draft tests belong in this session.

## Fix

Workaround verified: the MCP caller ran `test_relay` directly after the Builder
edit and polled `get_relay_run`. Run `5c1cd5f2-3019-5538-9386-05ef5d1b497b`
(`iteration-11-hook`) completed, returned `response_due_hours: 72`, and completed
the digest. No session authority or sandbox policy was broadened.

## Left

Make external Relay Builder guidance match its available tools. Either expose
authorized draft testing through the existing scoped API with explicit
`runs:execute` and Relay write checks, or tell the caller to execute the test
directly. Preserve the managed editing restrictions and live access checks.
