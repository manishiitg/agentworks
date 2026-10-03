# PULSE FIX PHASE

Run only after Technical Review has classified current workflow evidence, as
the caller-supplied Fix message in one retained Review+Fix sequence. Confirm the completed `technical_review` receipt for this conversation first;
if it is missing or incomplete, report the limitation and do not apply repairs.
Do not rerun Technical Review, Strategic Review, or broad
discovery. Do not treat raw workflow observations as repair issues.{{if .Focus}}

Focus especially on: {{.Focus}}. The focus affects priority only; it cannot
promote an observation or bypass evidence.{{end}}{{if .RunFolder}}

Use `{{.RunFolder}}` as the primary retained run when it is relevant to the
selected issue's proof boundary.{{end}}

1. Load and apply `read_skill(skills=[{"name":"builder-reference","path":"references/pulse-fixer-practices.md"}])`, then load and apply `read_skill(skills=[{"name":"builder-reference","path":"references/fix-verification.md"}])`.
2. Use `pulse_run_id="current"`. Answered operator decisions are never applied
   here: they are applied in the Builder chat where the owner can watch.
3. Read the saved Gate worklist and
   `get_pulse_state(view="backlog", detail="compact")` exactly once. Work
   through the whole index in impact order (goal-blinding and main-output
   issues first), requesting `detail="full"` only for the issue IDs you are
   about to work on. Read their typed review records, attempts, verification,
   and the relevant SQLite review notes when this run has one. Manual
   slash-command sequences may have only typed review state and no Markdown
   review note; do not treat that absence as missing review. The compact backlog's `issues` feed
   is the ordinary repair queue. Workflow observations are evidence
   from reviewers and must not be repaired unless a reviewer already promoted them.
4. Close every active issue in this pass, one coherent bundle at a time (a
   bundle may include several issue IDs when they share one root cause,
   compatible targets and one proof). Each issue ends fixed, not a problem,
   the user's decision (with the exact change ready to approve), or
   platform-owned; nothing is deferred. If the context runs out first, stop
   truthfully and the next fix run continues.
5. Apply each selected bundle and perform only proportional immediate checks
   before starting the next. Use the
   smallest complete safe repair with normal Workflow Builder tools. Record
   exact changed targets, attempts, dispositions, and proportional post-change
   checks per bundle. A successfully applied fix closes now. Use
   `changed_unverified` when stronger runtime proof is unavailable; do not wait
   for a producing run, schedule verification work, or keep it in the queue.
   A failed mutation or a check reproducing the defect remains active.
6. Record the repair attempt and each issue disposition, but do not write or
   replace either reviewer's terminal receipt. Review completion and repair
   outcome are separate facts. If no safe canonical objective exists, preserve
   the truthful terminal module results without manufacturing a repair. When this is
   the caller-supplied Fix message of a combined manual Review+Fix sequence,
   also call `record_pulse_result` exactly once for `technical_review` with
   `pulse_run_id="current"` and the truthful repair/no-safe-repair outcome.
7. Finish with a concise statement: issues closed (fixed, not a problem, sent
   to the user, platform-owned), changes and proof per bundle, and any issue
   still open because the context ran out.
