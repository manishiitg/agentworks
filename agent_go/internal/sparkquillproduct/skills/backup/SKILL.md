---
name: backup
description: Back up the family's learning workspace to the parent's own private Hugging Face Hub dataset repo — the ONE destination this family uses, via the `hf` CLI.
---

# Back up the workspace

Protect the family's data — materials, generated study/tests/reports, the progress
page, and conversations — by pushing it to the parent's OWN **private Hugging Face
Hub dataset repo**, via the `hf` CLI (already installed). This is the ONLY
destination this family uses — never suggest GitHub, Drive, S3, or anything else,
and never suggest a different destination "for images" — `hf upload` handles
binaries fine.

Same config vs status contract as `publish` elsewhere in this app:

- `backup.json` (workspace root) — declarative config: `enabled`, `repo_id`
  (the parent's HF username + a repo name, e.g. `janedoe/sparkquill-backup`).
- `backup/status.json` — the operational result of the last attempt: `state`
  (`verified`, `pending`, or `failed`), last attempt/success timestamps, any
  error, and the source hash that was actually backed up.
Never write status fields into `backup.json`.

## Steps

1. **Check the token.** The HF token lives in this app's own secrets store, not a
   bare `hf auth login` session — call `list_secrets` and look for one named
   `HF_TOKEN`. If it's missing, tell the parent plainly: create a **Write**-access
   token at https://huggingface.co/settings/tokens, then save it via `set_secret`
   (name it exactly `HF_TOKEN`) or Settings → Secrets. Don't proceed without it.

2. **Read config.** `cat backup.json`. If missing, or `enabled` isn't true, or
   `repo_id` is empty, this needs setup.

3. **First backup is ATTENDED — same rule as `publish`.** Never do the first
   (repo-creating) backup unattended: if this turn wasn't started by the parent
   directly asking (e.g. it's an automated Pulse check-in), do NOT ask questions
   into the void — just note ONCE that backup isn't set up yet ("ask your parent
   to say 'back up the workspace' to set it up"), don't repeat that note every
   cycle, and stop. If the parent IS present, ask for their HF username + a repo
   name (don't invent one), write `backup.json` with `enabled: true` and that
   `repo_id`, then continue below. Once `backup/status.json` shows `state:
   "verified"` from a real prior success, later backups (including Pulse's own
   automated ones) may run without asking again.

4. **Skip if unchanged.** Compute a source hash, e.g.
   `find . -type f -not -path './.git/*' -not -path './backup/*' -not -path './archive/*' | sort | xargs shasum 2>/dev/null | shasum`.
   If `backup/status.json` already shows this exact hash backed up successfully,
   skip and say so ("already backed up, nothing's changed") rather than re-uploading.

5. **Upload — one command does it all**, including creating the repo the first
   time:
   `hf upload <repo_id> . . --repo-type dataset --private --exclude "archive/*" --token "$SECRET_HF_TOKEN" --commit-message "backup <date>"`
   **Always exclude `archive/`** — activities the parent put away don't need
   to ride along on every backup; the parent can back those up
   separately if they ever want to. Never upload secrets — the secrets store and
   the parent PIN hash live outside the workspace already; keep it that way.

6. **Write `backup/status.json`** — `state` (`verified` for a confirmed successful
   upload, `failed` otherwise), last attempt + success timestamps, any error, and
   the source hash just backed up.

7. **Tell the parent** in plain words: backed up (or why not), and that it's their
   own private Hugging Face repo — nobody else can see it unless they make it
   public themselves.

8. **Then end the turn with `suggest_actions`** — "Tell the parent" above is not the last step.
