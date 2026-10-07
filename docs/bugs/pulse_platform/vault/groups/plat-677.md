[← vault / groups](index.md)

# PLAT-677: Vault group descriptions must not name people

| Field | Value |
|---|---|
| State | open |
| Priority | P2 |
| Product | vault |
| Area | groups |
| Summary | A Vault group description named two users ('excluding ashutoshknp12 and patelvaibhav122003'); every member saw it |

## What happened

## Fix

## Left

## Report

#agent_works, 2026-10-07 14:21 (Rakesh, Confida): Integrations → Tools & secrets → Vault shows the Confida Platform group as "Confida platform users with Linear and Langfuse access excluding ashutoshknp12 and patelvaibhav122003". An agent in the owner's chat wrote that description with `manage_vault_access` while setting up Vault on 2026-10-04. Group descriptions are shown to every member.

## Fix

The Vault admin refuses a group description (create or edit) that contains an email address or a workspace user's email local part, and says why: describe what the group is for; membership is set by members.

## Left

- The existing Confida Platform description still has the names: the owner edits it (Vault → Groups, or an admin chat with manage_vault_groups) to "Confida platform users with Linear and Langfuse access".
- `mutate_vault_db` (direct SQL on groups) is not covered by the check.
