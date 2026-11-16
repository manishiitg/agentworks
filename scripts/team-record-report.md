# Read-only duplicate Team/project report

One-off operator inventory for #2809 (part (d) of #2796). **No cleanup mode exists.**
It does not start the application, invoke provisioning, use logins, call a network,
read a database, inspect chat contents or rewrite any record. Run once per server
on a consistent private metadata snapshot, or rerun if live records change while
scanning. The report is private inventory, not suitable for public GitHub issues.

```sh
umask 077
python3 -B scripts/team-record-report.py \
  --state-root /path/to/private/state \
  --workspace-root /path/to/workspace \
  --server example \
  --directory-json /path/to/private/users-list-export.json > report.json
```

`--directory-json` is the ordinary administrator `users action=list` JSON export
(`users` containing `user_id` or `id`, and `username`). It is optional: without it,
owner/people IDs still appear but the coverage explicitly says account-name
checks are unavailable. **Never supply config/users.json or credential storage.**
An authentication-store-shaped input is refused; only identity labels from the
safe export appear in the report. Do not paste tokens or exports into GitHub.
The script itself only prints JSON; it does not create report files.

Inputs are server-owned `state/ownership/projects.json`, `state/team-folders/*/team.json`,
`state/crew-sync/<project>.json` and workspace `_users/*/chat_history/product-conversations.json`.
Unregistered Crew folders are listed with unknown owner/canonical identity; editable
folder names/manifests never establish ownership. Project files under `Crew/<registered folder>` are counted/stat'ed, never read
except the display-only `product.json` metadata. External local folder bindings
are not followed by this server inventory. Database filenames may contribute to
file counts, but database/WAL/SHM contents are never opened. Links are skipped.
The file count includes resources under the project root; it is not just visible
user files and should not be compared to a UI count with a different definition.
A missing/unreadable file count is `null`, never zero. `--max-files` bounds each
project scan (default100000); a capped count is explicitly a lower bound.

Output includes every registered Crew and orphan Team record, with its owner,
people (invited state retained), registered-agent metadata, total regular-file
count/bytes and file-modification timestamp. `last_activity` is labelled as the
latest **conversation registry update**, not claimed to be the last completed AI
turn. It stays unknown without such evidence. No credentials, messages, agent
purpose/instructions, file contents or auth metadata are emitted.

Findings:
- **Crew twins:** same owner and exact Mac source (Mac ID, else exact pair ID,
  plus peer project ID). A unique nonarchived binding is a canonical candidate;
  two current bindings are ambiguous. Different owners/sources never merge.
- **Numbered Team accounts:** safe directory labels ending `(2)` through `(N)`
  are name-only candidates. `canonical_by_source` cites valid reciprocal
  source/home/virtual-owner/deterministic-ID links; no group-wide canonical
  account is guessed from a display name, population, recency or file count.
- **Orphan/conflicting homes:** a stored source link fails its reciprocal checks.
  An execution-owner-shaped ID without a source is only a suspicion, not identity
  proof. Unregistered Team records are reported with unknown file location.
- **`· online` agents:** name-only candidates, never proof that a record is safe
  to delete. Keep their source keys/IDs for the later explicit review.

Ownership conflicts, missing/malformed metadata and unsafe paths are warnings or
a failed report, not evidence of emptiness. This inventory is deliberately
nontransactional: it does not lock, repair, change grants or declare deletion safe.
Canonical annotations are review evidence only. Operator AND each affected
record's owner must separately approve any later cleanup; this command cannot do it.

Focused check (synthetic fixture only):
`PYTHONDONTWRITEBYTECODE=1 python3 -B -m unittest discover -s scripts -p test_team_record_report.py`.
