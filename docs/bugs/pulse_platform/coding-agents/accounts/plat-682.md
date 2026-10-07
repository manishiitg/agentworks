[← coding-agents / accounts](index.md)

# PLAT-682: Clearer error when a project runs on someone's private account

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | coding-agents |
| Area | accounts |
| Summary | The 'private account' refusal did not say whose account it was or what to do |

## What happened

## Fix

## Left

## Report

#agent_works, 2026-10-07 15:51 (Saurabh, Excellence): co-owner of "Timouthy - Candidate Sourcing Pipeline" got "the account workflow Timouthy - Candidate Sourcing Pipeline is set to run on is private to its owner and not shared with you…". The workflow runs on Vaibhav's personal account (by design, a co-owner cannot use it unless it is shared with the workflow). Owner: improve the message users see.

## Fix

"Workflow X runs on <owner>'s personal <Codex|Claude Code|…> account, which only they can use. Ask <owner> to share it with workflow X (Providers → the account → Who can use it), or pick another account for it in Models."
