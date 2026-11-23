# Slot launches: one Linux account per user

On a multi-user Linux host, coding CLIs can run as per-user Linux accounts
("slots", e.g. `slot01`) instead of the app account, so one user's CLI cannot
read another user's files or the server's secrets — even if the CLI itself is
compromised. Slots compose with Landlock confinement: the slot account bounds
*who* the process is, Landlock bounds *which files* it can open.

Without slots, every CLI runs as the app account and isolation is only the
CLI sandbox. Slots are for shared hosts; single-user installs don't need them.

## Enabling

1. Provision slot accounts with `provision-slots.sh` (in the main
   `mcp-agent-builder-go` repo, `deploy/rootless-linux/`). It creates the
   accounts, the root-owned allow-list
   `/usr/local/libexec/agentworks/slotctl.json`, the `slotctl` launcher, and
   the sudo rule letting the service account run `slotctl` as a slot.
2. Set `AGENTWORKS_SLOT_CLI=on` in the service environment.
3. During rollout, set `AGENTWORKS_SLOT_CLI_USERS` to a comma-separated list of
   user ids to move users over gradually; unset it when everyone is on slots.

## Configuration reference

`slotctl.json` (root-owned; `AGENTWORKS_SLOTCTL_CONFIG` overrides its path in
tests only):

| Key | Meaning |
|---|---|
| `slot_table` | Path to the JSON user→slot table (`{"slots": {"<user-id>": "slot01"}}`) |
| `slot_prefix` | Slot account prefix (default `slot`); accounts are prefix + 2–3 digits |
| `slot_run_root` / `slot_state_root` | Per-slot run and state folders (`<root>/<slot>/`) |
| `slot_docker` | `true` when every slot has its own rootless Docker (below) |
| `docs_root` | Docs root, used by the legacy folder fallback |

Environment (service process only; never per-request):

| Variable | Meaning |
|---|---|
| `AGENTWORKS_SLOT_CLI=on` | Master switch for slot launches |
| `AGENTWORKS_SLOT_CLI_USERS` | Rollout limiter (comma-separated user ids) |
| `AGENTWORKS_SLOT_PREFIX` | Slot prefix when several products share one host (below) |
| `AGENTWORKS_SLOTCTL` | Alternate `slotctl` path for a product with its own slot accounts |

## Who runs what: declared identity

The application declares each launch's identity per turn (`llmtypes.RunAs`):

- **Declared with a user** — the launch runs as that person's slot. A Crew
  reader's turn names the Crew *owner*, not the reader.
- **Declared without a user** — the launch runs as the app account (a Goal).
- **Not declared** — the provider falls back to the old folder rule (a folder
  under `<docs>/_users/<id>/` runs as that user's slot) and logs it. Moving a
  project to a shared folder silently loses isolation this way, so callers
  should always declare.

The host's slot table wins disagreements: if the application names a slot the
table doesn't confirm for that user, the launch is **refused** with
`[SLOT_EXPLICIT_MISMATCH]` — it never silently falls back to the app account.
Declared app-account launches and canary-excluded users keep no-slot behaviour.

## Several products on one host

Each product gets its own slot accounts and launcher so products never share
slots: set `AGENTWORKS_SLOT_PREFIX` (e.g. `AGENTWORKS_SLOT_PREFIX=confida`
matches `confida01`, `confida02`, …) and `AGENTWORKS_SLOTCTL` to that product's
launcher. The config file's `slot_prefix` wins over the variable when both are
set. Slot detection, run folders, and user→slot lookup all scope to the
product's own prefix.

## Per-slot Docker

With `slot_docker: true`, a command run as a slot gets
`DOCKER_HOST=unix:///run/user/<slot-uid>/docker.sock` — the slot's own rootless
Docker. The platform account's `DOCKER_HOST` is replaced, never passed through,
so a slot can neither reach nor share the platform's containers. If the slot
user lookup fails, the environment is left unchanged and Docker is unavailable
to that launch rather than shared.

## How a launch works

The host writes the launch (argv, cwd, environment) to a 0660 JSON request
file in the slot's run folder, then runs
`sudo -n -u <slot> slotctl exec --request-file <file>`. The CLI keeps its
normal stdin/stdout; the request file is removed after use. Launch files and
temp folders for a slot launch live under that slot's run folder, not the
shared temp dir.

## Troubleshooting

- `[SLOT_EXPLICIT_MISMATCH] … no confirmed slot for that user` — the user has
  no row in the slot table. Add them (and re-run provisioning if the account
  doesn't exist).
- `[SLOT_EXPLICIT_MISMATCH] … table says "slot07"` — the application resolved
  a different slot than the table. The table wins; fix the application's
  mapping or the table.
- `slot launch blocked` surfaced to users — same mismatch family; check the
  declaration (turn logs) against the table.
- Docker unavailable inside a slot launch — the slot has no rootless Docker
  running, or `slot_docker` is off while the platform socket stays unreadable
  to slots. Either way the slot never shares the platform's Docker.
- Launches still run as the app account — check the master switch, the rollout
  user list, and that the config path is readable.
