[← integrations / slack](index.md)

# PLAT-694: Slack bot removal refusal names the workflow and its owners

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | integrations |
| Area | slack |
| Summary | Removing a Slack bot that another workflow still uses gave an unclear refusal; the person could not tell which workflow or who could fix it |

## What happened

## Fix

## Left

## Report

#agent_works, 2026-10-07 16:53 (Rakesh, Confida): removing his bot con-sb-test (`slack_7d68df6e`, workflow confida-sb-test) failed. The server answered 409 "slack connection is still selected by workflow \"testing\"…": the `testing` workflow (owned by others, not him) has that bot selected as its Slack bot. His chat agent then tried raw curl calls.

## Fix

The refusal reads "This bot can't be removed yet: workflow testing (owners: …) still uses it. An owner of that workflow must choose another bot in its Integrations → Slack first."
