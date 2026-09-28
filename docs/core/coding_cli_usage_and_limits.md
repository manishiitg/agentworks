# Coding CLI usage and limits

Crews and workflows run on coding CLIs (Claude Code, Codex, Cursor, Pi, Muse).
Each CLI has its own plan limits, and AgentWorks shows and uses what the CLI
reports about them.

## What you see: the terminal icon in the chat

Hover the terminal icon next to the chat box (or the live-view icon on a
read-only run tab). Below "Open live view · <model>" it lists what the CLI
reported for this session:

```
5h 17% · resets 3:30 PM          plan window, % used, reset in your timezone
7d 35% · resets Thu 10:00 AM     longer plan window
Context 42%                      how full the model's context window is
Session: 1.2M in (900k cached) · 36k out · $4.10
xhigh · pro                      reasoning effort, plan
```

- Lines appear only when the CLI reported them, usually after its first reply.
- A plan window at 90% or more is highlighted.
- The small lime chip next to the icon shows the same plan windows at a glance.

| CLI | Plan windows | Context | Tokens | Cost |
|---|---|---|---|---|
| Claude Code | yes, after the first reply | yes | yes | yes |
| Codex | yes | yes | yes | — |
| Pi | no (API keys, no plan windows) | — | yes | yes |
| Cursor | no (no local source) | — | estimated | — |
| Muse | not yet (only over its MSP host) | — | — | — |

## How limits are handled

When a CLI hits its plan limit it stops and waits. Two checks catch that so a
chat or run does not hang:

- **While a reply is awaited**, the Claude adapter fails the turn as a quota
  error with the reset time. It trusts Claude's own usage numbers first.
- **The coding watchdog** checks every CLI terminal every few seconds, so
  background sessions are covered too. It stops a session only when:
  - the CLI's own usage numbers (Claude, Codex) show a window used up; or
  - there are no such numbers yet, and a limit notice from the CLI itself is
    on screen twice in a row.

  It ignores the user's messages and the assistant's replies. Words like
  "we got rate limited" in a message are not a limit.

## Keeping it working: the P0 contract

Every CLI provider declares whether it reports plan usage, and where from. If
it cannot, it states why. The P0 release run proves it for Claude and Codex
with a real turn (`CertPlanUsage`). A new provider cannot skip the question.
Source: `multi-llm-provider-go` `coding_agent_contract.go`
(`SurfacesPlanUsage`, `PlanUsageSource`, `PlanUsageUnavailableReason`).
