[← coding-agents / accounts](index.md)

# PLAT-666: Providers shows Needs Authentication for a working account

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | coding-agents |
| Area | accounts |
| Summary | A working provider still shows Needs Authentication in Providers |

## What happened

The provider was Antigravity (`agy-cli`): Confida's Goals chats ran on `agy-cli`
(gemini-3.8-flash-high) all day on 2026-10-06, while the server account was no longer reported as
configured after the 13:42 release (its identity status probe stopped running, which only happens for a
configured server account).

Cause: the host runs agy in API-key mode: no stored Google login in the service HOME, and
`GEMINI_API_KEY` in the service environment. Every agy chat passes `GEMINI_API_KEY`/`GOOGLE_API_KEY`
into the CLI (agycli `agySidecarKeyEnvVars`). The login probe behind Providers (`agy models`, from
`providerAuthConfigured` via the manifest and the account list) ran with `minimalChildEnv()` and no key,
so agy answered "Please sign in" and the badge said Needs authentication. Same identity and HOME as the
chats (the app account; agy is not a slot launch), different environment. The minimal-env change of
2026-10-01 gave every other CLI probe its documented key variable but not agy.

## Fix

- `agent_go/cmd/server/multiagent_llm_tools.go`: `agyCLIProbeEnv()` = minimal environment plus
  `GEMINI_API_KEY` and `GOOGLE_API_KEY`, the same keys agy chats get. No other secret is passed.
- Test: `TestAgyCLIProbeEnvCarriesTheChatGeminiKeys`.

## Left

- Not verified live. After the next Confida deploy, Providers should show Antigravity as Connected.
- The Providers badge still reflects only the server account; a person whose chats run on their own
  account sees the server account's state there (the account rows below are per account).

## Report

#agent_works, 2026-10-06 19:11 (Confida, after the Gmail connect fix): chats worked, but Providers still showed the account as Needs Authentication. Check whether the status probe runs as the same identity/home as the chat (slot accounts).
