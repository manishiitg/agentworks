# Acceptance run (manual, not part of a deploy)

A fixed set of real messages you send as a test user to check that a server works end to end: the model answers, shell commands
run inside the sandbox, a person cannot read the server's secrets or other people's folders, a burst of messages is not rate
limited, Brain and token-usage tools respond. `catalog.json` lists the cases; each says what the answer must (or must not) contain.

The cases are plain tool calls (`ask_crew`, `crew`, `brain_update`, `brain_read`, `account`), so they run the same two ways:

## 1. The script (the CLI)

```
agentworks --server https://<server> login        # once, as the test user (a browser sign-in)
deploy/acceptance/run.py --server https://<server> [--area sandbox] [--slots]
```

`--slots` includes the cases that need per-user Linux accounts (the account must be a slot, other people's folders must be
closed). The first run creates one scratch Crew, "QA acceptance". Exit code 1 means a case failed; each failure prints the
answer that broke the rule.

## 2. Through MCP (an agent that has the server connected)

Connect the server to the agent (Claude Code: `claude mcp add --transport http agentworks-<name> https://<server>/api/external/v1/mcp`,
then sign in as the test user). Then tell it:

> Read `deploy/acceptance/catalog.json`. Find or create the Crew named in `crew` (tool `crew`, action list/create). For every case:
> if it has `tool`, call that tool with `args`; if it has `message`, send it with `ask_crew` to that Crew (wait_seconds 25, then poll
> `functions` action=status with the call_id until it finishes); for `burst`, send that many `ask_crew` calls at the same time.
> Check each answer against the case's `expect` (must match) and `forbid` (must not match) regular expressions. Skip cases with
> `needs: slots` unless I said the server has slots. Report one line per case, PASS / FAIL / SKIP, with the answer text on a FAIL.
> Do not change anything else on the server.

The catalog is the single source: the script and an MCP agent read the same file, so adding a case adds it to both.

## What it does not cover

Code chats cannot be driven through the API today (the external tools expose Crews, workflows, Relays, Brain and Vault; Code is
read-only for reviewers), so the Code-specific checks stay a browser checklist. A Crew shares the same sandbox and slot path as
Code, which is why the shell and isolation cases use one.
