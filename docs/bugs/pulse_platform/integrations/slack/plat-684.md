[← integrations / slack](index.md)

# PLAT-684: Stale Slack test for scheduled runs

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P3 |
| Product | integrations |
| Area | slack |
| Summary | A Slack policy test expected scheduled runs to get the Slack settings tool; the product policy deliberately gives them none |

## What happened

## Fix

## Left

## What happened

`TestSlackToolsFollowSessionOriginAndReadOnlyPolicy/schedule` failed on main. It expected a scheduled (cron) Builder
turn to get `get_slack_bot_settings`. Since 2026-09-17 (1a325c5f1) `product.yaml` gives the scheduled origin no
`bot_management` capability, so scheduled turns get no Slack bot tools at all. The code is right; the test was stale.

## Fix

The test now expects no Slack bot tools for scheduled turns.
