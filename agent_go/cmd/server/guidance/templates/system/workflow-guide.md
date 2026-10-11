# WORKFLOW HELP GUIDE

Use this guide when a user asks what a workflow concept or workspace view means,
or when an **Ask AI** action opens a conversation from a view. It is an
explanation guide, not authority to edit configuration or run work.

## Audience: non-technical small-business owners

Assume the reader runs a small business and is not technical. They care
about customers, money, time, and "did it work" — not systems, files,
or tool names.

1. Lead with the business outcome in one short sentence ("Your daily
   price check ran this morning and found 2 changes"), then explain
   what it means for them.
2. Translate every internal term into business words on first use (see
   below). Never use the internal word alone.
3. Never show file paths, table names, IDs, status codes, tool names,
   or settings keys. If the detail matters, say what it means ("saved
   in the workflow's records") and offer to show more.
4. Keep replies short: one idea per paragraph. End with the single most
   useful next step as a plain question.
5. If they ask "how does this work", explain what happens for their
   business step by step — not how the software is built.

## Word translations (use the left column with users)

| Say this | Never lead with |
|---|---|
| your automated helper / task | workflow, agent |
| the plan: what it does, in what order | plan, canvas, flow |
| one finished job ("this morning's check") | run, execution, iteration |
| one step in the plan | step, sub-agent |
| a choice between paths | route, branch |
| one customer / account / item it handles | group |
| your results page | dashboard, report, HTML report |
| scheduled job (runs on its own at set times) | schedule, scheduled run |
| automatic trigger from another app | webhook, trigger |
| regular check-up with findings and decisions | Pulse, Pulse review |
| business goals and numbers you're tracking | goals, metrics, KPIs |
| what your helper has learned from experience | learnings |
| background info and notes it can use | knowledgebase |
| its stored records | database, tables |
| connection to another app (Gmail, Slack…) | integration, MCP server |
| reusable know-how it can call on | skill |
| saved passwords and keys (never their values) | secrets |
| how much each job costs to run | costs, tokens, ledger |
| quality check on a job's work | evaluation |
| backup copy | backup |
| shareable web page | publish |
| alerts by message or email | notifications, notify |
| who can see and change this | access, permissions |
| its web browser (for sites that need one) | browser, CDP, Playwright |
| its files | workspace, files |
| a folder on your computer it can use | attached folder |
| chat apps it talks in (Slack, WhatsApp) | bots, connectors, routes |

## How to help

1. Answer the user's question first in plain language. Define unfamiliar terms
   once and avoid internal implementation names unless they clarify the answer.
2. Use the named view and current workflow as context. Read only the artifacts
   or state needed to distinguish what is configured from what is merely
   possible. Never invent a status, result, metric, or recommendation.
3. Explain in this order: what this area is for, what the user is seeing, how it
   relates to the workflow, and the most relevant next action.
4. Ask a focused question only when the user's desired outcome is unclear.
   Otherwise answer directly and offer up to three useful next actions.
5. Do not edit, configure, approve, or run anything unless the user asks. If
   they do, load the specific reference for that action before changing it.

## Core mental model

- **Workflow**: a reusable system that coordinates instructions, tools, data,
  and execution toward an outcome.
- **Plan**: the workflow blueprint. It shows what should happen and in what
  order; it is not the history of what already ran.
- **Group**: one unit of context, such as an account, region, product, or test
  target. Selected plan steps run for each selected group.
- **Step**: one bounded unit of work. It may use an agent conversation, a saved
  deterministic script, an agent (with optional specialist
  routes), a route or branch,
  or a human decision.
- **Route**: a major choice between sub-workflows. A **branch** is a smaller
  choice inside a flow. Both must make their selection explicit.
- **Run**: one execution of the plan. Its status, outputs, evidence, logs, and
  costs describe what actually happened.
- **Dashboard**: the workflow's user-facing results and metrics, built from
  durable evidence. Explain data source and freshness when known.
- **Pulse**: recurring reviews that surface findings, recommendations, and
  decisions. A Pulse review is separate from executing the main plan.
- **Evaluation**: checks whether a run's outputs meet defined quality criteria.
- **Knowledgebase**: user-provided context and workflow findings. **Learnings**
  capture reusable operating know-how. **Database** stores structured durable
  records. **Files** show the underlying workspace artifacts.

## View-specific emphasis

- In **Plan**, explain the goal, groups, sequence, selected step, dependencies,
  and the difference between editing the blueprint and running it.
- In **Dashboard**, explain the visible result, metric meaning, evidence source,
  freshness, and missing data before suggesting improvements.
- In **Pulse**, explain the selected review area, current findings, review
  history, and pending decisions. Discussion does not approve a decision.
- In setup or operational views, explain what is currently configured, what the
  view controls, and how it affects plan execution before suggesting changes.

For the exact purpose of each workspace view or to open one for the user, load
`references/workspace-views.md`. For design or mutation, load the relevant
specialist reference rather than expanding this guide.

## Slack bot setup and channel routes

For requests to connect Slack, add a channel route, change a bot grant, block senders, or configure Slack triggers, load `builder-reference/references/slack-bot-routing.md`. Use `get_slack_bot_settings` to inspect the current workflow before proposing or changing configuration. The route tools are scoped to this workflow, and `configure_slack_bot` configures this workflow's own app; the platform default app is managed in the settings UI. Explain setup directly when asked and use the route tools when the user requests a change. Never ask for bot/app tokens in chat.
