# Antigravity CLI (agy) setup

AgentWorks uses the locally installed `agy` CLI (Google Antigravity) as a
coding-agent provider. This is a different product from `gemini-cli`: do not
substitute one's config, key, or login for the other's.

## 1. Install

```bash
curl -fsSL https://antigravity.google/cli/install.sh | bash
```

Verify the install and check the version against the certified floor (the
providers panel reports supported/unsupported automatically):

```bash
agy --version
agy models
```

`agy models` lists models only when authenticated, so it doubles as the
login check below. Certified CLI: 1.2.12.

## 2. Sign in (interactive)

`agy` has no `login` subcommand: Google sign-in completes inside the TUI.
Either launch it in a terminal:

```bash
agy
```

and follow the Google sign-in (copy-paste the URL/code when prompted), or
use the providers panel: open the Antigravity CLI entry and run the
**Authenticate** action, which opens the same guided terminal. No SSH or
direct server access is required.

First launch also shows a one-time theme picker, and each new workspace
folder asks "Do you trust the contents of this project?". Trust is
exact-path: trusting a folder does not trust its subdirectories. AgentWorks
trusts the exact Chat or private workflow directory it creates before AGY
boots. For a separately selected directory, confirm its prompt yourself;
AgentWorks turns fail loudly if that trust gate remains open.

## 3. Verify in AgentWorks

The providers panel entry flips to **Connected** when the runtime is on
`PATH` and authenticated. Models come from `agy models`; the default is
`gemini-3.8-flash-high`, and reasoning effort is baked into the model slugs
(`-high`/`-medium`/`-low` suffixes).

The existing **Native agent tools** switch also applies to Antigravity. With
it off, AGY uses the MCP bridge for tools. With it on, AGY can use native file
read/search and web read/search tools; commands, writes and subagents still
use the bridge. AgentWorks installs a temporary `.agents/hooks.json` entry in
the AGY workspace for this gate and restores the prior hook file after the
session closes. This mode needs `python3` on the backend `PATH`.

AGY is marked **Alpha** in the UI. Its tmux terminal shows progress during a
turn, while the assistant reply and tool receipts appear after the turn from
AGY's structured conversation records.

## 4. API-key mode (unattended / CI)

Interactive Google sign-in cannot run headless. For CI and servers, point
agy at the Gemini API directly with both of these (the variable alone has
no effect):

```jsonc
// ~/.gemini/antigravity-cli/settings.json
{ "modelProvider": "gemini" }
```

```bash
export GEMINI_API_KEY="<key from https://aistudio.google.com/apikey>"
```

Notes, verified against agy 1.2.12:

- agy never reads `.env` files; export the variable in the process
  environment (or the CI secret store).
- `GOOGLE_API_KEY` is ignored; only `GEMINI_API_KEY` is read.
- API-key mode bills the key, not the Antigravity subscription, and needs
  no sign-in. `/logout` does not affect it.
- A managed key alone does not authenticate the provider: the
  `modelProvider` settings flip is part of the setup, which is why the
  manifest reports auth from the CLI's own login state rather than from a
  stored key.

## 5. Quota

Antigravity Starter subscriptions carry a small model quota shared across
everything on the account (one full provider-certification run can exhaust
it). When it is gone every model call fails with `RESOURCE_EXHAUSTED /
Individual quota reached` until the reset window passes; `agy models` keeps
working because listing models is auth-gated, not quota-gated.

agy exposes no quota slash command, so the providers panel has no usage
action for it: limit responses surface during runs instead. Sustained or
unattended use should run in API-key mode (§4).

## Troubleshooting

- `login required` fail-fast: the stored login is missing (or a headless
  run has no API-key mode). Sign in (§2) or configure §4.
- `agy TUI blocked on a workspace trust gate`: confirm the trust prompt
  for that exact folder.
- `Please sign in to view available models` from `agy models`: not
  authenticated. Note `agy models` exits 0 even here: read the text, not
  the exit code.
