# Build, Share, Use: one model for every product

Status: product model, 2026-10-14. Applies to Goals/workflows, Crew, Relays and
Code. New features are designed against it; existing per-product wiring
converges on it over time.

The goal is that people go from Build to Share to Use as quickly as possible.
Judge every feature by whether it shortens that path; anything that slows it
down is a cost. Runs are real work: running a step or a workflow, from the
Builder, MCP or a schedule, has real effects, with no simulation or dry-run
mode unless someone asks for one. Protection comes from the existing access
rules (sharing, Builder/Run, Vault, connection scopes), not from extra gates,
confirmations or holds added on top. Ship and fix forward.

## The model

Every product follows the same three stages.

| Stage | People | Agents and machines |
|---|---|---|
| **Build** | The website Builder | MCP: coding agents (Claude Code, Codex, Cursor and others) using AgentWorks tools |
| **Share** | App chat, dashboards and shared links, Slack, WhatsApp (Gmail planned) | The MCP network, webhooks |
| **Use** | Asking or pressing published actions through those channels | Their own agents over MCP, schedules, Watchers, other products |

### Build

Someone builds the thing: a workflow, a Crew, a Relay, a Code project. The
website Builder and MCP are equal surfaces. Anything that can be built or
changed in one can be built or changed in the other, under the same
Builder/Run rules and the same server checks. A new feature ships with its MCP
tool, not only a screen.

### Share

The built thing exposes its entry points once. Share channels are adapters onto
those same entry points, never separate product code. Turning on a new channel
for something that already works elsewhere is configuration, not a rebuild.

### Use

People use what was shared through those channels, and through callers they
own: their agents over MCP, their schedules and their Watchers. A
schedule or a trigger is a caller owned by a person. It runs with that person's
current access and selected secrets, can call only what is shared with them,
and its cost is attributed to them. The target keeps its own execution rules
and credentials; using a caller never adds authority. Access is rechecked at
run time, on every call.

## Two kinds of communication

Every product is reached through two entry points
([communication protocols](../communication-protocols.md) describes the
current contracts in detail):

| | Open text: `ask` | Structured: functions, routes, triggers |
|---|---|---|
| What it is | A conversation in plain words | A named operation with typed inputs and a tracked result |
| Who it suits | People, and agents that need judgement | Machines: schedules, triggers, webhooks, other products |
| Mode | Runs as the caller in Run mode | Runs under the target's published contract |
| Result | Replies in the caller's conversation | A `call_id`, status and a typed result |

`ask` is the single conversational access point. The app chat of a non-owner,
dashboard Ask (including shared links), Slack mentions, WhatsApp, MCP ask and,
later, Gmail should all reach it through the same server path (WhatsApp's path
still needs confirming against this rule). A channel only formats
and delivers; it never decides what is allowed. Before answering from files,
an ask checks the product's shared functions and prefers one that fits. It runs
a function directly only when the function says it is read-only, and otherwise
tells the person what will happen and waits for a yes.

Functions, routes and triggers are the structured access point. Their inputs
are validated against the target's schema, a stable submission ID makes
retries safe, and the event that started a call can never choose the target.

## Products talking to each other

Products communicate the same way. A Crew, workflow, Relay or Code agent that
needs another product is one more caller of that product's `ask` or
functions/routes, acting for the person its run acts for. Messages between
agents (Bullpen) are one more channel. No product reads or writes another
product's files or internals directly.

## Rules that follow

1. One server path per entry point. Every channel and every caller goes through
   the same admission, Run/Builder checks and attribution.
2. A channel declares how it presents (threads, edits, progress). It never
   carries authorization; see [channel connectors](../channel-connectors.md).
3. Callers are owned by people. Their cost and history belong to that person.
4. Targets keep their own policy. A caller is admitted by its own access; the
   target then runs under its published contract and configured credentials.
5. Relays are targets only. They expose published functions and have no
   timers of their own.

## Where things stand

- Built: Crew and workflow functions callable from workflows, Code and MCP;
  published Relay functions; Slack and WhatsApp connectors; dashboard Ask for
  Crew, workflow and Relay dashboards and shared links; the Run-mode ask
  checking functions first; agent-to-agent messages.
- Available in Code: Watchers, person-owned Python checks that start a Code
  agent or call an authorized function, workflow route or internal trigger
  without a model turn for the check. Crew and workflow sources follow.
- Planned: Gmail as a channel, with every shared agent getting its own inbox;
  Ask on Code dashboards; per-product schedules, webhooks and Slack wiring
  converging on the shared entry points.

Related: [functions and calling](functions_and_calling.md),
[communication protocols](../communication-protocols.md),
[channel connectors](../channel-connectors.md).
