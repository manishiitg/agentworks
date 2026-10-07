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

Guidance, not a code check (owner: a names filter in code makes no sense): the Vault agent's prompt and the `manage_vault_groups` description parameter say a group's name and description are shown to every member, describe what the group is for, and never name people, emails or exclusions. (A gateway-side name check was added in a241f249e and reverted.)

## Left

- The existing Confida Platform description still has the names: the owner edits it (Vault → Groups, or an admin chat with manage_vault_groups) to "Confida platform users with Linear and Langfuse access".
