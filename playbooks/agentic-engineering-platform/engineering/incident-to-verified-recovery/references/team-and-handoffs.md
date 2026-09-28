# Incident to Verified Recovery handoffs

The Investigation Crew owns incident facts and hypotheses. The Delivery Crew owns change and release state. Both outputs refer to the same stable incident ID and service ID. Another Crew may fill both slots only when its access is compatible; keep two independently validated step outputs.

## `incident-investigation/v1`

Required fields: `artifact_type`, `incident_id`, `service_id`, `environment`, `window_start`, `window_end`, `owner_id`, nonempty `timeline` (each event has `event_id`, `observed_at`, `source_ref`, `summary`), `hypotheses` (each has `statement`, `confidence`, `evidence_refs`), and `proposed_actions` (each has stable `action_id`, `owner_id`, `approval_state`, `next_evidence`). Unknown impact or cause stays explicitly unknown. The owner reviews the action before a change route starts.

## `engineering-blocker-ledger/v1`

Required fields: `artifact_type`, the same `incident_id`, `service_id`, and `environment`, `owner_id`, `change_id`, `issue_ref`, `commit_sha`, `ci_ref`, `deployment_ref`, `delivery_state`, `approval_state`, `source_refs`, and `next_evidence`. Any unavailable source is `null` and blocks a claim that depends on it. `delivery_state` distinguishes `proposed`, `in_progress`, `ci_passed`, `deployed`, and `verified`. Only source-backed fields can advance the state.

Reject a handoff if the incident or service differs, a timeline event lacks a source, a proposed action lacks an owner, the delivery step invents an approval, or a `ci_passed` state is presented as recovered. Source records and production outcomes need owner review even after structural validation.

## Recovery decision

The report includes the customer-approved service indicator, threshold, environment, observation window, telemetry references, and `recovered`, `not_recovered`, or `unknown`. A rollback or fix is an action receipt; recovery is a later observation. Incident closure is a separate authorized owner decision.
