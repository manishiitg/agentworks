# Acceptance run (manual, not part of a deploy)

A fixed set of checks you run as a test user against a deployed server: the model answers, shell commands run inside the sandbox as
the person's own Linux account, a person cannot read the server's secrets or other people's folders, a burst of messages is not rate
limited, and (as an admin) the admin tools, Vault, schedules, dashboards and Relays work. `catalog.json` lists the cases; each says
what the answer must (`expect`) and must not (`forbid`) contain. It is meant to be re-run by hand after a deploy or a server change;
deploys do not run it.

Two kinds of case:

- **Message cases** (`message`): a real chat turn to a scratch Crew through `ask_crew`, so they go through the model, the tmux session, the
  sandbox and the person's slot. Every shell case must print a marker (`CMD-RAN`, `shell-ok`) or a slot name, so a command that never ran
  cannot pass. The rest are failures if the answer mentions a runner or permission error.
- **API cases** (`tool`/`steps`, marked "(API only)" in the output): direct tool calls that check permissions and bookkeeping. They run no
  chat.

## Run it (the script)

```
agentworks --server https://<server> login [--scopes ...]     # once per test account; browser sign-in as THAT account
deploy/acceptance/run.py --server https://<server> [--slots] [--admin] [--area A] [--crew-name NAME]
```

| Flag | Meaning |
|---|---|
| `--slots` | The server runs per-user Linux accounts: include the cases that need them (own account, no other users). |
| `--admin` | The test account is an administrator: include the admin cases (users, Vault, Code review, Crew info, schedule, dashboards, Relay). |
| `--area` | Only one area: `model`, `sandbox`, `isolation`, `brain`, `limits`, `access`, `admin`. |
| `--config` | The test account's saved login (`agentworks --config PATH login`). Keep one file per account (admin, member) and both stay signed in side by side. |
| `--code [TITLE]` | Send the message cases to the account's own Code project (tool `code`) instead of a Crew. Needs a login with `--scopes ...,code:run`. Names the project, else the first one. Local-mode projects are refused. Burst and the API cases are skipped. |
| `--crew-name` | Name of the scratch Crew (default "QA acceptance"). **Use a different name for each test account.** A Crew's folder admits only its owner's slot, so another account's shell commands in it fail with "permission denied". |

Sign-in scopes: the default CLI scopes do not include Brain or Relays. For the member run use
`--scopes workflows:read,files:read,runs:execute,crews:read,crews:run,crews:write,knowledgebase:read,knowledgebase:write,relays:write,dashboards:read`.
An administrator's default login already gets every scope. Make sure the browser is signed in as the account you mean to test (the sign-in
page does not ask), and check the server log's username afterwards.

Exit code 1 means a case failed. A failure prints the answer that broke the rule. SKIP means a precondition is missing (for example the
account has no Brain folder).

## Two accounts side by side

```
agentworks --config ~/.config/agentworks/<server>/admin.json  --server https://<server> login
agentworks --config ~/.config/agentworks/<server>/member.json --server https://<server> login --scopes <member scopes>,code:run
deploy/acceptance/run.py --server https://<server> --config .../admin.json  --slots --admin
deploy/acceptance/run.py --server https://<server> --config .../member.json --slots --crew-name "QA acceptance member"
deploy/acceptance/run.py --server https://<server> --config .../member.json --slots --code <project title>
```

The browser sign-in uses whoever is signed in to the site, so sign out (or use a private window) before approving the member login, and check the
server log's username. The same two accounts can be added to an agent as two MCP servers with different names.
`code:run` is never in a default login (it lets a token run commands in the person's Code project).

## What a failure usually means

| Output | Look at |
|---|---|
| `could not start ... permission denied` | The account cannot enter the Crew/project folder (wrong test account for that Crew), or the release folder/launcher is not executable by slots. Run `./deploy.sh slotcheck <server>`. |
| `SANDBOX_UNAVAILABLE` | The host restricts user namespaces and the AppArmor exception is missing: `provision-slots.sh userns`; workspace health must say "private /tmp available". |
| `own-account`: no `slotNN` | The account has no slot: `provision-slots.sh ensure`. |
| `no-secrets` / `no-other-users` print content | Isolation is broken. Stop and report. |
| `burst` rate limited | The model gateway's limit; the answer text names the status. |
| `access denied`/`insufficient_scope` | The sign-in scopes, or the account's products (users.json); the message names the scope it needs. |

## Add a case

Edit `catalog.json` (the script and an MCP agent read the same file):

```
{"id": "my-case", "area": "sandbox", "needs": "slots", "label": "what it proves",
 "message": "Run `cmd; echo CMD-RAN` and reply with exactly what it printed.", "expect": "CMD-RAN", "forbid": "runner failed|denied permission"}
```

`needs` is `slots` or `admin`. An API case uses `tool` and `args`, or `steps` (each with `tool`, `args`, `expect`, optional `capture` regexes
whose groups become `{v.NAME}`, `sleep`, `optional`, and `cleanup: true` for steps that must run even after a failure).

## Through MCP (an agent that has the server connected)

Connect the server to the agent (Claude Code: `claude mcp add --transport http agentworks-<name> https://<server>/api/external/v1/mcp`,
then sign in as the test user). Then tell it:

> Read `deploy/acceptance/catalog.json`. Find or create the Crew named in `crew` (tool `crew`, action list/create). For every case:
> if it has `tool`, call that tool with `args`; if it has `message`, send it with `ask_crew` to that Crew (wait_seconds 25, then poll
> `functions` action=status with the call_id until it finishes); for `burst`, send that many `ask_crew` calls at the same time.
> Check each answer against the case's `expect` (must match) and `forbid` (must not match) regular expressions. Skip cases with
> `needs: slots` unless I said the server has slots, and `needs: admin` unless I am an administrator. Report one line per case,
> PASS / FAIL / SKIP, with the answer text on a FAIL. Do not change anything else on the server.

## What it does not cover

- Code chats are driven with `--code` (the `code` tool, scope `code:run`): the same shell and isolation cases run in a real Code project (Dev or
  Cowork). Not covered: Local mode (runs on a laptop), the toolbar and modes UI, side chats of a Code project.
- Browser flows (sign-in, the account menu, the new-project dialog), Gmail connect, and Local mode on a laptop.
- Running as a Crew reader (an account that is not the Crew's owner): checked by hand (a member asks the admin's scratch Crew to run a command;
  it runs as the Crew owner's slot, PLAT-810), not in the catalog.
