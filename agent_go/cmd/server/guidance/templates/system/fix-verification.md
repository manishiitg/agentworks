## Fix evidence and recurrence monitoring

Treat a successfully applied bounded technical repair as fixed and close its
issue immediately. Assume it remains fixed unless new evidence reproduces the
defect. Do not run a verification campaign, wait for scheduled execution, or
create a follow-up task merely to strengthen proof. Confirm that the mutation
succeeded and use a small relevant immediate check when practical; no blanket
requirement to execute the full workflow or exercise every consumer.

This is the single contract for honestly recording the evidence available when
a bounded repair is applied. It is
the same standard whether the fix is applied inside scheduled AgentWorks, a fix run,
a Technical review started from AgentWorks tab, or an approved measurement
change. Load it
before applying a fix. Issue closure records the applied repair; a verified
claim separately records a check that actually passed. A failed mutation or an
immediate check that still reproduces the defect must remain active.

### Post-change evidence boundary

Every fix is judged against a **post-change evidence boundary**.
Immediately before a mutation, record the mutation start time, the canonical
target identity and the key/record changed, the pre-change hash or version, and
the latest relevant pre-change run/artifact ids. Everything produced before that
boundary is **baseline only, never proof** that the change works — an older
successful artifact is baseline, not verification.

### Optional immediate verification

Do not pursue stronger verification merely to close an issue. If claiming
`fixed_verified`, accept proof only from one of:

- **(a)** a side-effect-free deterministic check run *after* the mutation that
  exercises the changed canonical state through its **real runtime consumer
  path** — not the store inspected in isolation; or
- **(b)** a fresh execution, eval, or report artifact created *after* the
  mutation that carries matching run, step, target, and provenance.

Verify that the real runtime consumer actually reads the changed canonical
store: a successful write alone is not proof. File existence, mtime alone, a
successful write, or rereading an older successful artifact is **not** proof.

### When proof needs a future run

If stronger proof requires an externally side-effecting run or the next
scheduled producing run, do **not** trigger that run merely to verify. Record
the successfully applied repair as `changed_unverified`; the issue closes under
the issue-register lifecycle. A later normal run that produces the same
semantic concern must reuse the existing `issue_id`, append its evidence, and
reopen it. Do not create a verification-only run or a second issue. Do not add a
future `next_check` solely for this applied repair, or select it in a later review
merely because another run occurred. Keep the evidence limitation in the repair
record, not in a pending-work queue. Historical pre-fix artifacts are not recurrence.
