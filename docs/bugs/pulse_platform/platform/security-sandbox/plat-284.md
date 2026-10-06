[← platform / security-sandbox](index.md)

# PLAT-284 — Packages a step installed were thrown away with the run folder, so every run reinstalled them from PyPI

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | platform |
| Area | security-sandbox |
| Summary | P2 — PLAT-283 made installs *possible*; this makes them |

| Coordination | Value |
|---|---|
| Assigned agent | Claude Fable 5.1 |
| Ticket state | `fixed`, live-verified on the Dominion deployment |
| Last synchronized | `2026-09-05` |

- **Priority:** P2 — PLAT-283 made installs *possible*; this makes them
  *sensible*. Without it every run pays a full PyPI download (~15 packages
  for `yfinance`) and inherits PyPI/network/version-drift risk on every
  single run, forever — the trading agent's own follow-up objection, and a
  correct one.
- **Owner:** `workspace/security/sandbox_tool_env.go` (routing, all three
  backends), `agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/controller_agent_factory.go`
  (`setupExecutionFolderGuard` grant), `agent_go/pkg/workspace/advanced_tools.go`
  (tool description), guidance `code-authoring.md` / `backup-strategy.md`,
  `agent_go/cmd/server/workflow_backup.go` (hash skip).
- **Related:** PLAT-283 (installs at all), PLAT-281 (deployment-installed
  CLIs in `/srv/dominion/tools` — the box-level complement to this).

## What was found

PLAT-283's routing chose the step's `WorkDir` as the base for pip's user
site and cache. Live on Dominion that is
`runs/iteration-0/default/execution` — the per-run folder that rotates on
the next run — so an install survived exactly one run. The agent's framing
("a completely fresh, disposable sandbox with nothing carried over") was
half wrong, though: the *policy* is per-run, the filesystem is not. The
same live `[SHELL ISOLATOR]` line shows the workflow root
`Workflow/tectonicusadaytrading` in the step's write grant, and it persists.
There was already a place that survives runs; nothing pointed pip at it.

Two related facts that shaped the fix: a step can never install
system-wide (`/usr` is read-only to it and the service user has no sudo —
that is the sandbox doing its job), so "persistent" can only mean
per-workflow; and the default step grant is only its own step folder +
Downloads + db — the trading workflow's root-wide write is the exception,
so the persistent folder needs an explicit grant to work everywhere.

## What shipped

- One reserved folder per workflow, `Workflow/<id>/.sandbox-cache/`
  (`security.SandboxPersistentDirName`). `setupExecutionFolderGuard` grants
  it writable to every execution step (and generic agents), so it is
  materialized like every other platform-managed grant on first use.
- The sandbox recognises the folder by name in the write grant — the two
  modules only meet over HTTP, so the name is the contract, no new request
  plumbing — and routes into it: `PYTHONUSERBASE`, `PIP_CACHE_DIR`,
  `XDG_CACHE_HOME`, `npm_config_cache` + `npm_config_prefix`, `GOPATH` /
  `GOCACHE`, `CARGO_HOME`, `PIPX_HOME` / `PIPX_BIN_DIR`; its `bin/`,
  `python/bin`, `npm-global/bin`, `go/bin`, `cargo/bin` lead `PATH`; and
  `SANDBOX_PERSISTENT_DIR` names it so an agent can put a venv or a
  downloaded binary there deliberately. `TMPDIR` stays in the run folder —
  scratch must not accumulate in the one place that survives. Without the
  grant (older orchestrator, non-workflow caller) PLAT-283's run-folder
  fallback stands unchanged.
- The routing moved out of the Landlock-only file into a shared
  `sandbox_tool_env.go` and is applied by all three backends (Landlock,
  mount namespace, macOS Seatbelt), so a workflow installs the same way on
  a dev Mac as on a Linux box — the macOS default profile never blocked
  `$HOME`, which is why none of PLAT-281/283/284 ever reproduced locally.
- Agents are told: the `execute_shell_command` description and the
  code-authoring guidance now say installs work and persist, distinguish
  `$SANDBOX_PERSISTENT_DIR` (tooling) from `db/assets/` (files), and say
  root/`apt` is not possible from a step.
- Backups skip it (`shouldSkipBackupHashFile`, alongside `runs/`), and the
  backup-strategy guidance lists it with the other never-back-this-up
  folders.

## Verification

- Unit: `sandbox_tool_env_test.go` (persistent routing, run-folder
  fallback, unwritable WorkDir, no-grant no-op, persistent-only TMPDIR, the
  `Isolator.toolEnv` path the macOS/mount backends use) run on macOS and,
  cross-compiled, on Dominion; the folder-guard suite with a new subtest
  pinning the grant; `TestBackupHashSkipsTheSandboxCache`.
- Live on Dominion after deploy: see the register row / the end of this
  ticket for the two-run proof — run 1 `pip install --user yfinance`
  downloads and installs into `.sandbox-cache/python`; run 2, in a fresh
  run folder, `pip install --user --no-index yfinance` succeeds —
  `--no-index` forbids any network, so it can only pass if the install
  truly persisted.
