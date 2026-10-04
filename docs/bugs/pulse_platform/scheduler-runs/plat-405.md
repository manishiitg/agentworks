[← Platform issue index](../../pulse_platform_issue_register.md)

# PLAT-405 — Deploy behaviour: drain, Slack notices, release pruning, one runtime profile

| Coordination | Value |
|---|---|
| State | fixed on `main` (deploy scripts only); all commits are in the builds of Excellence `agents-0cf68aa9` / `agents-e7db4f50` and Confida `confida-23270875`; step 2 (`85209b1da`) is not in Dominion `5e15f373` or SparkQuill `sparkquill-49a1e676`, so the standard profile is written to those on their next rootless-linux deploy |
| Severity | P2 |
| Date | 2026-10-03 |
| Owner | scheduler-runs |
| Related | `docs/design/deploy_unification.md`, release "exit 7" fix (`docs/DECISIONS.md` 2026-10-01) |

Commits: `aa70c8802` / `4be06d911` Slack opt-in, `6e2995c38` Slack on again, `5e15f373d` Slack silent again, `310ca9e6d` drain, `bdd643bd4` pruner,
`7fc2ae975` report (step 1), `85209b1da` profile (step 2).

## Slack deploy notices (three reversals the same day)

`deploy.sh` posted "deploying" and "finished" for every deploy, including false "problem" notices from the early health probe (fixed 2026-10-01). 1) Opt-in
(`DEPLOY_SLACK_NOTIFY=1`). 2) On by default again, `DEPLOY_SLACK_NOTIFY=0|false|no|off` silences a run; checked against a local fake receiver (default success 2
messages, default failing run 2 messages, `=0`/`=off` none, `=1` 2; exit code kept; both start and finish respect the switch). 3) Owner "for now make Slack
posts silent": notices only with `DEPLOY_SLACK_NOTIFY=1` (current). The webhook is `DEPLOY_SLACK_WEBHOOK_URL` or `~/.config/agentworks/deploy-slack-webhook`; none set, nothing sent.

## Switch over at once

Owner "force deploys for now". `deploy.sh` passes `DEPLOY_DRAIN_SECONDS` (default 0) into the build job as `DRAIN_TIMEOUT_SECONDS`, so build-and-activate.sh's drain restarts
immediately; `DEPLOY_DRAIN_SECONDS=300 ./deploy.sh <server>` waits up to 5 minutes. A running turn is cut off by a deploy. Side effect seen right after deploys: "Unable to load
project browser sessions" and "Could not stop the session" were the 502 window while the agent restarted; both work on retry.

## Old releases never pruned

Confida kept 15 releases (14 GB), Excellence 5: the pruner keeps any release with a `.deploying` marker, the rootless deploy removes it on its very last line, so a
deploy that exited after the release went live (the false "exit 7") left it behind (14 of 15 Confida, 4 of 5 Excellence; all finished, none in use). Done by hand:
stale markers removed, pruned with `--keep` for the two newest previous releases (Confida 15 -> 3, 10 GB; Excellence 5 -> 3), both Go build caches cleared (7 GB + 5 GB,
rebuilt by the next deploy), `/etc/logrotate.d/agentworks` set (100 MB, 3 copies). The pruner now ignores a marker older than 6 hours
(`test_a_stale_deploying_marker_does_not_pin_a_release`). The shared Hetzner disk hit 100% on 2026-10-02 (issue #260); free space is now 71 GB.

## Deployment unification

Step 1: `deploy/common/runtime_profile.json` is the standard profile; `./deploy.sh report [server]` prints how each running server differs (agent and workspace process
environment, private /tmp, kept releases, release source, slots/bin on PATH). Report only; the rootless-linux deploy prints it at the end and never fails on it. First run:
every server differed (state root, MCP state dir, browser profile mostly unset; SparkQuill without MULTI_USER_MODE; SparkQuill, Dominion and RTS without the CLI lock
settings; RTS not native; Excellence and Dominion keep one release).
Step 2: `build-and-activate.sh` reads the profile and writes every `same_everywhere` setting into `.env` and both services (like EXTRA_ENV, verified in the running
processes): NATIVE_WORKSPACE, CDP off, CLI lock and Full CLI, AGENTWORKS_STATE_ROOT, AGENTWORKS_MCP_STATE_DIR (state/mcp on every product; a release's MCP user config is
carried over once), AGENT_BROWSER_SHARED_PROFILE=`<app>/state/browser-profile`. Excellence, Confida, SparkQuill. MULTI_USER_MODE stays per server (SparkQuill is single-user,
data in `_users/default`; switching it on would hide that data). No rollback step (owner: "if anything goes down it's fine").

## Left

- Step 2 on Dominion, SparkQuill and RTS (deploy pending).
- Remaining steps of `docs/design/deploy_unification.md` (one script for all servers).
