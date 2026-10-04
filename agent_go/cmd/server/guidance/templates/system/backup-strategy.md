## Backup Strategy — Workflow Git + Large-File Storage

Each workflow has (or should have) its own remote git repository for
text/code/config plus a separate object store for large binary artifacts.
This skill is the playbook for deciding what goes where, how to commit
safely, and how to use a large-file backend like HuggingFace Hub when git
is the wrong tool.

If the workflow does not yet have a remote git repo configured, recommend a
private GitHub repository (or another off-device destination) first, then ask
the operator before initialising one — repo creation is a one-time setup
decision (account/org, visibility, naming). A local Git commit is the ordinary
temporary rollback checkpoint, but it is not a durable backup because it is
lost with the laptop. Never describe local-only backup as healthy off-device
protection.

### Local snapshot containment (mandatory)

The zero-configuration local checkpoint is the **Git commit itself**. Do not
create a Git bundle, ZIP, tarball, or other snapshot inside the workflow/source
repository. In particular, never write `backup/*.bundle` and then add it to the
same repository: each later bundle contains the earlier repository history, so
tracking it creates a recursive growth loop. The shell guard rejects relative
or in-repository `git bundle create` destinations.

Do not create a standalone repository archive merely because backup is due.
If an operator explicitly asks for one, resolve both paths first and require
the destination to be an absolute path under the platform backup root and
outside the source repository. Generate to a temporary external path, run
`git bundle verify`, then atomically replace the previous verified artifact.
Never stage or commit it in the source repository. Otherwise, record the local
commit as `local_only` and configure a real off-device destination for
durability.

## App contract: config vs status

The app reads workflow backup configuration and backup health separately. Keep these separate.

For a **workflow**:

- `workflow.json.backup` is the declarative contract: whether backup is
  enabled, which trigger should run it, which destinations exist, and what
  each destination covers.
- `backup/status.json` is the operational result of the latest backup
  attempt. Update it after every configure or backup attempt, including
  partial and failed attempts.
- Do not write frequently changing status fields into `workflow.json`.
- **Edit workflow.json safely:** change only the `backup` block, preserve every other field,
  and after writing re-read it with a JSON parser
  (`python3 -c "import json; json.load(open('workflow.json'))"`) — a malformed workflow.json
  drops the workflow's config and can hide the workflow from the UI.

Recommended backup config shape (`workflow.json.backup`):

```json
{
  "enabled": true,
  "mode": "agent",
  "triggers": {
    "after_scheduled_run": true,
    "after_manual_run": false
  },
  "destinations": [
    {
      "id": "config-repo",
      "type": "git",
      "provider": "github",
      "repo": "owner/workflow-backup",
      "branch": "main",
      "covers": ["workflow", "planning", "knowledgebase", "learnings"],
      "secret_refs": []
    },
    {
      "id": "artifacts",
      "type": "object_store",
      "provider": "r2",
      "bucket": "workflow-artifacts",
      "prefix": "workflow-id/",
      "covers": ["runs", "media", "large-artifacts"],
      "secret_refs": ["R2_ACCESS_KEY_ID", "R2_SECRET_ACCESS_KEY"]
    }
  ],
  "notes": "Human-readable restore and provider notes."
}
```

Required backup status shape (`backup/status.json` for workflows, `pulse/backup/status.json`
for org):

```json
{
  "version": 1,
  "state": "healthy",
  "last_attempt_at": "2026-06-14T10:30:00Z",
  "last_success_at": "2026-06-14T10:30:00Z",
  "last_agent_session_id": "workflow-backup-...",
  "last_source_hash": "...",
  "summary": "Pushed config repo and synced artifacts.",
  "destinations": [
    {
      "id": "config-repo",
      "type": "git",
      "provider": "github",
      "state": "healthy",
      "last_success_at": "2026-06-14T10:30:00Z",
      "commit": "abc1234",
      "objects_synced": 0,
      "summary": "Committed workflow config and pushed origin/main."
    }
  ],
  "last_error": "",
  "updated_at": "2026-06-14T10:30:00Z"
}
```

Status values:

- `local_only`: a local Git commit (or an explicitly requested external local
  recovery artifact) exists, but no off-device destination is configured. The
  app intentionally shows this in red.
- `healthy`: all required configured destinations succeeded.
- `partial`: at least one configured destination succeeded and at least one
  failed or was skipped.
- `failed`: no required configured destination succeeded.
- `configured_not_verified`: backup is not fully set up or could not be
  verified yet.

## What goes where

Use git when content is small, mostly text, benefits from per-line diffs,
and needs to be cheap to clone:

- `workflow.json`, `planning/plan.json`, `planning/step_config.json`
- `knowledgebase/`, `learnings/`, `subagents/`, `skills/`
- Small JSON metadata in `db/` (post records, run summaries)
- Documentation and notes
- Source code, scripts, configs

Use a large-file backend (HuggingFace Hub or any S3-compatible bucket)
when content is binary, regeneratable, large per file, or large in
aggregate:

- Conversation dumps (`builder/conversation/*.json`, often 10-30 MB each)
- Per-iteration run artifacts (`runs/iteration-*/`, can be GB)
- Installed packages (`.sandbox-cache/`, hundreds of MB, recreated by any run's
  `pip install --user` — never back this up at all; put it in `.gitignore`)
- Generated images (`db/posts/visuals/*.png`, `*.jpg`)
- Generated audio/video (`*.mp3`, `*.mp4`)
- Model checkpoints, trained weights, vector stores
- Anything over ~25 MB per file
- Anything that pushes a single git repo over ~500 MB total

Verify the **per-workflow git repo's visibility** before backing up workflow
content. A private repository does not by itself authorize copying secrets.
Do not skip the non-secret backup just because the workflow has a password:

```
gh repo view <owner/repo> --json visibility -q .visibility   # PRIVATE | PUBLIC
```

- **Private GitHub repo** — back up non-secret workflow content. Keep
  `secrets.json`, `workflow_secrets/`, credentials, and keys out of Git, just
  as for a public repository. Preserve secrets through the designated secret
  system; if recovery requires a separate export, use only an explicitly
  approved, supported secure export process, not plaintext staging.
- **Public repo, unknown visibility, or ANY large-file backend** — never
  commit/push secrets (`secrets.json`, `workflow_secrets/`, `*.key`, `*.pem`,
  `.env*`, `credentials*`, `*.token`). Back up everything else and keep
  secrets out (use the workflow secret tools — see `secret-management`).
- **Never, regardless of visibility** — operator / global (env-backed
  `GLOBAL_SECRET_*`) credentials, OAuth tokens, or API keys that are not the
  workflow's own; and PII unless the destination is explicitly approved.

The presence of a secret file is never a reason to abandon the backup: at
minimum commit the authorized non-secret content and report any unresolved
secret-recovery dependency without disclosing values.

This is **enforced**: a `git push` carrying secret files to a confirmed-public
GitHub repo is hard-blocked by the shell guard (private / unknown / non-GitHub
remotes are allowed). So pushing secrets to a private repo just works; pushing
them to a public one is refused — make the repo private or keep the secrets out.

## Git workflow per per-workflow repo

Run Git backup commands in the **parent workflow-builder/Pulse turn only**.
Do not delegate backup to a reviewer or another sub-agent. Delegated tool agents have deliberately narrower
write access and cannot write the workflow's `.git/` directory; attempting Git
there produces misleading `FETCH_HEAD` / `index.lock: Operation not permitted`
errors. This is a sandbox boundary, not a macOS ownership or mount problem.

### Managed SQLite backup (mandatory when `covers` includes `db-sqlite`)

The live `db/db.sqlite` database and its WAL/SHM sidecars are protected runtime
state. Shell and generic file tools must not read, copy, stage, or unlock them.
Before backup, use `create_workflow_database_snapshot` unless the scheduler has
already supplied a current managed-snapshot receipt in the finalizer message.
The workspace service reads SQLite through the database engine, includes all
committed WAL rows, runs `PRAGMA integrity_check`, and atomically publishes the
result at `backup/database/db.sqlite` with a stable checksum sidecar at
`backup/database/db.sqlite.sha256`.

Stage both managed files for a destination that covers `db-sqlite`.
Do not stage `db/db.sqlite`, `db/db.sqlite-wal`, or `db/db.sqlite-shm`. Treat the
managed snapshot as the database restore image and document that restore copies
it back to `db/db.sqlite` while the workflow is stopped. If snapshot creation
fails, record the database destination as partial/failed, then continue to
Publish and Notify; backup failure never suppresses those independent actions.

For a legacy repository that already tracks the live DB, migrate the Git index
once without deleting the runtime file: add `db/db.sqlite`, `db/db.sqlite-wal`,
and `db/db.sqlite-shm` to `.gitignore`, then use
`git update-index --force-remove -- db/db.sqlite db/db.sqlite-wal db/db.sqlite-shm`.
Stage `.gitignore`, the managed snapshot, and its checksum explicitly. Confirm with
`git ls-files -- db/db.sqlite db/db.sqlite-wal db/db.sqlite-shm` (it must be
empty) and `git ls-files -- backup/database/db.sqlite backup/database/db.sqlite.sha256`
(it must name both managed files). Never use `rm`, `git rm` without `--cached`, or another command that
could delete the live database.

Assume the workflow folder is itself a git working tree with a single
remote pointing at the per-workflow repo. Common operations:

```
cd <workflow_root>

# Status before doing anything
git status -sb
git log --oneline -5

# Fetch before integrating — another laptop / CI may have advanced origin
git fetch origin

# If origin advanced, integrate it before creating the backup commit. Do not
# pull/rebase over an unexplained dirty tree; preserve and inspect user changes.
git rebase origin/<branch>

# Stage explicit paths, not git add -A (avoids accidental binary/secret)
git add planning/plan.json knowledgebase/notes/<file>.md

# Inspect exactly what will enter history. Backup archives (*.bundle, *.zip,
# *.tar, *.tgz) must never appear here.
git diff --cached --stat
git diff --cached --name-only

# Commit message: imperative subject + why (1-2 sentences in body)
git commit -m "knowledgebase: add reddit-post recipe

Captures the patterns that worked in iteration-7. Cited from
runs/iteration-7/default/execution/.../report.md."

# Push
git push
```

### When to commit

Prefer one commit per logical unit of work, not one giant catch-all:

- After a planning change (`plan.json`, `step_config.json`) — separate
  commit so the diff is reviewable.
- After a knowledgebase update — separate commit with what was learned.
- After a workflow.json config change (servers, skills, secrets list) —
  separate commit so config history is grep-able.
- Avoid committing transient state mid-run (token usage, in-flight
  conversation files). Those belong in `.gitignore`.

### Conflict and race handling

- If `git pull` hits a merge conflict in a JSON file, **do not text-merge**
  — parse both sides, decide intent, write the merged JSON, then
  `git add` and finish the merge. Workflow JSONs (plan, step_config,
  workflow.json) are structurally sensitive.
- If `git push` is rejected because the local branch is behind, pull
  the remote refs with `git fetch`, then rebase only the unpublished local
  backup commit(s) onto `origin/<branch>`, resolve any conflicts, re-run the
  backup verification, and push. Never use `git reset --hard`, discard a dirty
  tree, or force-push a shared workflow branch.
- If multiple processes write to the same workflow folder, batch their
  commits — frequent tiny commits from a sync loop can create the same
  "race" merges that plagued the old shared workspace-docs repo.

### Hooks

Never bypass pre-commit / pre-push hooks with `--no-verify` or
`--no-gpg-sign` unless the operator explicitly asks for it. A failing
hook usually points at a real issue (committed secret, oversized file,
lint failure) — fix the issue rather than skip the gate.

## Large-file backends — pick one per workflow

Decide a backend once per workflow and persist the choice in
`workflow.json.backup.destinations`. When chatting with the user, offer the
options below and ask which they prefer — defaults vary by content type and
existing account access.

| Backend                         | Strongest fit                                  | Egress cost      | Free tier         | Auth env var              |
|---------------------------------|------------------------------------------------|------------------|-------------------|---------------------------|
| **HuggingFace Hub**             | ML datasets, models, audio/image/video         | Free (HF CDN)    | Unlimited public  | `HF_TOKEN`                |
| **Cloudflare R2**               | Generic blobs, web-served assets               | **$0** to anywhere | 10 GB storage    | `R2_ACCESS_KEY_ID` / `R2_SECRET_ACCESS_KEY` |
| **Backblaze B2**                | Cheap cold backup, archival                    | Free egress via Cloudflare; otherwise paid | 10 GB storage     | `B2_KEY_ID` / `B2_APP_KEY` |
| **AWS S3**                      | Existing AWS stack, deep IAM                   | Paid             | 5 GB / 12 months  | `AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY` |
| **Google Cloud Storage**        | Existing GCP / Vertex AI stack                 | Paid             | 5 GB / 12 months  | `GOOGLE_APPLICATION_CREDENTIALS` (JSON path) |
| **Azure Blob Storage**          | Existing Azure stack                           | Paid             | 5 GB / 12 months  | `AZURE_STORAGE_CONNECTION_STRING` |
| **rclone (multi-backend)**      | Anything above + ~70 more, one CLI for all     | n/a (proxy)      | n/a               | Per-remote in `~/.config/rclone/rclone.conf` |

Store the secret as a workflow secret (see `secret-management`) so it
lands in `$SECRET_<NAME>` at runtime; never echo or commit it.

### HuggingFace Hub

Strongest for ML-shaped data: datasets, model weights, generated audio /
image / video. Single-file limit 50 GB. Public free, private requires
Pro.

```
export HF_TOKEN="$SECRET_HF_TOKEN"
hf auth whoami                                  # confirm
hf upload <owner>/<repo> <local> [path-in-repo] --repo-type dataset
hf upload <owner>/<repo> ./db/posts --repo-type dataset \
  --exclude "*.json" --exclude "**/temp/*"
```

Each upload is a git commit on the HF side — durable, revisionable
(`--revision <branch>`).

### Cloudflare R2

S3-compatible API, **zero egress cost** anywhere (the standout feature
vs S3/GCS/Azure). Free tier 10 GB storage, 1 M Class A ops / month.
Best when artifacts are served back to clients (websites, dashboards)
or downloaded frequently.

```
# Use AWS CLI (rclone or s3cmd also work); R2 endpoint is per-account
aws s3 cp local.png s3://<bucket>/<key> \
  --endpoint-url https://<ACCOUNT_ID>.r2.cloudflarestorage.com
aws s3 sync ./db/posts s3://<bucket>/posts \
  --endpoint-url https://<ACCOUNT_ID>.r2.cloudflarestorage.com
```

Configure credentials with `aws configure set` or via
`AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY` env vars. Create the
R2 bucket and access key in the Cloudflare dashboard (one-time).

### Backblaze B2

Cheapest at-rest pricing (~$6/TB/month). Free egress to Cloudflare,
paid elsewhere. Best for cold backup / archival that rarely gets
downloaded.

```
b2 authorize-account "$SECRET_B2_KEY_ID" "$SECRET_B2_APP_KEY"
b2 sync ./db/posts b2://<bucket-name>/posts
b2 upload-file <bucket> ./file.bin path/in/bucket/file.bin
```

### AWS S3

Most conventional choice when the workflow already uses AWS. Mature
tooling, deep IAM, predictable pricing but paid egress.

```
aws s3 cp local.png s3://<bucket>/<key>
aws s3 sync ./db/posts s3://<bucket>/posts --exclude "*.tmp"
```

Auth via `aws configure` or env vars (`AWS_ACCESS_KEY_ID`,
`AWS_SECRET_ACCESS_KEY`, optional `AWS_SESSION_TOKEN` for STS).

### Google Cloud Storage

Best fit when the workflow already uses Vertex AI / Gemini / other GCP
services so the service account can be reused.

```
export GOOGLE_APPLICATION_CREDENTIALS=/path/to/sa.json   # one-time
gsutil cp local.png gs://<bucket>/<key>
gsutil -m rsync -r ./db/posts gs://<bucket>/posts
```

### Azure Blob Storage

Best fit for an existing Azure stack.

```
export AZURE_STORAGE_CONNECTION_STRING="$SECRET_AZURE_STORAGE_CONNECTION_STRING"
az storage blob upload --container-name <ct> --name <key> --file local.png
az storage blob sync -c <ct> -s ./db/posts -d posts
```

### rclone (one CLI for all of the above)

When the workflow may switch backends or needs to mirror to two
destinations, configure `rclone` once and use the same syntax across
backends. Supports HF, S3, R2, B2, GCS, Azure, Dropbox, Google Drive,
and ~70 others.

```
rclone copy ./db/posts <remote-name>:<bucket>/posts
rclone sync ./db/posts <remote-name>:<bucket>/posts --exclude "*.tmp"
```

Remote definitions live in `~/.config/rclone/rclone.conf` (one block
per remote). Use `rclone config` once interactively, or write the
config file directly from a secret.

### When NOT to use any large-file backend

- The data is genuinely transient — per-iteration scratch, intermediate
  parses, in-flight tool output. Don't back it up; let the workflow
  regenerate.
- The data is sensitive and the destination's privacy posture hasn't
  been verified for that workflow's content. Ask the operator first.

## Decision matrix

| Content                                         | Backend                  |
|-------------------------------------------------|--------------------------|
| `workflow.json`, `planning/`, `knowledgebase/`  | git (per-workflow repo)  |
| `learnings/`, `skills/`, `subagents/`           | git                      |
| Small JSON metadata in `db/`                    | git                      |
| Generated images, audio, video                  | HF dataset (or S3/R2/B2) |
| Conversation dumps, per-iteration run logs      | HF dataset, or skip      |
| Model checkpoints, embeddings, vector stores    | HF model/dataset         |
| Secrets, credentials, OAuth tokens              | secret store — never git |
| Transient parses, in-flight scratch             | nothing — regenerate     |

When in doubt, check the file's size (`du -h`) and ask: would I want a
1-line diff for this content? If yes, git. If no, large-file store.

## Recovery / restore checks

After any backup-touching change, verify:

- `git status` is clean (no uncommitted edits the operator was relying
  on).
- `git log origin/main..HEAD` is empty (nothing stuck unpushed).
- For HF uploads, `hf repo info <repo_id> --repo-type dataset` shows
  the expected file count and total size; sample-download one file and
  diff against local to confirm round-trip integrity for the first push
  of a new destination.
