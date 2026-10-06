[← goals / pulse / findings](index.md)

# PLAT-154 — `record_pulse_finding` reports failure after successfully updating a finding owned by another module

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | goals |
| Area | pulse/findings |
| Summary | P1 — the durable update succeeds, but the false error makes the |

| Coordination | Value |
|---|---|
| Assigned agent | Codex |
| Ticket state | `implemented` — regression test passes; live restart/retry verification pending |
| Last synchronized | `2026-08-19` |

- **Priority:** P1 — the durable update succeeds, but the false error makes the
  reviewer retry, wastes tool/model work, and can cause the terminal module
  receipt to describe a valid evidence update as incomplete.
- **Owner:** typed Pulse finding write/reload boundary.

## Evidence and RCA

The Social Media Engineering/Ops child updated existing issue `PUL-42183990`
three times and received:

```text
recorded Pulse finding could not be reloaded by its internal lifecycle identity
```

The live SQLite row proves the cross-module shape:

```text
fingerprint       step_id                phase   status
4218399017332805  eval-workflow-success  review  open
```

The reviewer correctly supplied the visible issue ID and module
`workflow_review`. `RecordPulseReviewFinding` resolved that ID to the existing
fingerprint, preserved `step_id=eval-workflow-success`, wrote the recurrence and
typed details, then tried to verify the result through
`LoadPulseFindingLifecycles(..., marker.Module, ...)`. That module-filtered view
cannot contain the step-owned row unless a separate fix-attempt link happens to
exist. The successful write therefore became a false tool error.

## Fix and reasoning

The post-write verification now reloads the unfiltered lifecycle view and
matches the exact already-resolved fingerprint. The fingerprint is the internal
row identity; the current observer's module is not. This does not expose the
fingerprint to agents or weaken public `PUL-…` identity validation.

A regression test first records a finding under `eval-workflow-success`, then
updates that public issue from `workflow_review`. It proves that:

1. the call returns the same public issue ID;
2. the original step identity remains intact;
3. recurrence increments rather than creating a second issue; and
4. the typed reviewer evidence is attached to that existing row.

## Acceptance

1. Updating a step-owned issue from a reviewer module returns success.
2. Same-module updates remain idempotent and keep one lifecycle row.
3. No internal fingerprint appears in the public tool result.
4. A live post-restart Pulse update of `PUL-42183990` produces no retry loop.
