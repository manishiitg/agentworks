[← platform / security-sandbox](index.md)

# PLAT-400 — Supply-chain loader ("PolinRider" family) ran on the Excellence box and the public scanner missed it

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | platform |
| Area | security-sandbox |
| Summary | contained; sweep tool on main. |

| Coordination | Value |
|---|---|
| State | sweep tool on main; incident contained; owners of the infected accounts and repos still to act |
| Severity | P1 (credential-stealing loader running as three Linux users on a shared box; not in AgentWorks code) |
| Date | 2026-10-03 |
| Owner | security-sandbox |

## What happened

22 long-running `node -e global['_V']=…` processes on the Excellence box (20 as `python`, 1 as `pythonai`, 1 as `node`) fetched and ran
code from `166.88.134.62`. Not AgentWorks code: none of the `agents`, `confida`, `sparkquill` or `dominion` accounts carried it.

Source for `pythonai` (`/home/pythonai/Scott_chatbot`): a developer's commit `c944a2b` (2026-07-29) grew `frontend/postcss.config.mjs`
from 94 bytes to 9,314 bytes, the payload appended to the build config, which the build tool executes. The server pulled it on 2026-08-17 10:18
and the process started on the next build (11:03). Commit `47e8826` (2026-09-17) cleaned the file, but the process kept running from memory and
the history still holds it. The `python` processes (since 2026-06-22) have no matching file today; their source is unknown.

## Why the public scanner said "No infections found"

It matches two fixed signatures of an older variant, reads only today's copy of 8 config file names, skips `.git` and `node_modules`, and looks at no process.
This variant (`global['_V']`, `_$_16d1`) and the already-cleaned file defeated all three.

## Done

- Contained 2026-10-03 (owner approved): 22 processes killed, outbound `ufw deny out to 166.88.134.62`, evidence in `/root/incident-20261003-node-loader/` (root only).
- Sweep tool `supply-chain-sweep.sh` (read-only, with its test) kept outside this repo, in the owner's `~/ai-work/security/` folder (owner decision 2026-10-04).
  It checks running `node -e` processes, connections to loader servers, every text file (build output and `node_modules` included), the full git history of
  every repo, and config files far above normal size that also run dynamic code.

## Left

- Owners of the `python`, `pythonai` and `node` accounts and of the GitHub account `nodeexcel`: scan their other repos, rotate every key and token used on those accounts.
  The `Scott_chatbot` git reflog holds a GitHub token in a remote URL: revoke it.
- Find the source of the `python` account's 2026-06-22 processes.
- Run the sweep on a schedule per server and at deploy (not wired yet).

## Register notes

[PLAT-400](plat-400.md), P1, contained; sweep tool on main. Account owners must rotate
credentials; scheduled sweep not wired yet.
