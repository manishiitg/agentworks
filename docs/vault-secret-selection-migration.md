# Legacy secret selections: one-time Vault migration

Vault rejects a selected secret that has no stored value. Older installations
could retain those selections while silently supplying an empty runtime value.
The migration reconciles these old attachments without changing secret values.

## Deployment and local startup

Vault-enabled rootless deployments run `server migrate-secret-selections
--apply --once` after stopping the old agent and before starting the new one.
The backend also runs the same migration before opening its HTTP listener on
Vault-enabled local/server launches, covering installers without that hook.
It skips subsequent starts after successful completion.

The migration scans canonical workflow and project manifests under Workflow,
Crew, Relays and the per-user project roots (including Crew and Code). It does
not recursively scan executions, source repositories, release snapshots or
backups. No installation-specific secret names are hardcoded.

For each selected name in `selected_secrets` and
`selected_global_secret_names`, it checks:

1. The project's shared encrypted store and legacy stores for its owners.
2. The encrypted managed global store.
3. `GLOBAL_SECRET_*` settings from the deployment environment.

Only a name absent from all applicable stores is detached. A same-named secret
in another project does not count. Existing global secrets are retained even if
the current user has no group grant: permission denial is not a missing value.
Existing encrypted records are retained even when corrupt or empty, so a
decryption failure cannot silently detach a credential. Those records still
need repair if runtime validation rejects them.

## Operator command

Run as the service account with the same environment as the backend, including
any `GLOBAL_SECRET_*` settings. Stop the agent before applying manually.

```sh
agent server migrate-secret-selections --docs-root /absolute/docs --state-root /absolute/state
agent server migrate-secret-selections --docs-root /absolute/docs --state-root /absolute/state --apply --once
```

The first command prints a dry-run report without writing anything. It works
even after the one-time marker exists, so it can check for later stale edits.
The second applies the plan and writes a completion marker. Omitting `--once`
from an explicit apply allows an operator to reconcile later stale selections;
normal runtime requests still reject them and never silently clean them up.

## Backup, failure and recovery

Every changed manifest is backed up byte for byte under
`state/migrations/secret-selections-v1/backups-*/`. The directory is private
(0700), and backup/report files are 0600. `report.json` maps each original
manifest to its backup and lists removed names. No values or encrypted blobs
appear in the report. Unknown manifest fields, numeric precision, null
selections and unrelated configuration are preserved.

The complete scan finishes before the first write. Missing document mounts,
malformed/unreadable stores, unsafe symlinks or malformed manifests stop the
migration. Existing manifests and ciphertext remain untouched on a scan
failure. An apply failure retains backups and does not write
`completed.json`; the next run can resume. A deployment hook failure stops
activation. A local startup failure logs the error and leaves normal admission
checks in force, retrying on the next start.

To restore a changed manifest, stop the agent and use the report to copy its
backup to the recorded manifest path. Do not remove the completion marker
unless you intend the cleanup to run again. Reattaching a missing name does not
create its value; securely supply that value in the correct project or Vault
if it is still needed. The migration does not move/promote values, create
aliases, copy another project's credentials or grant Platform access.
