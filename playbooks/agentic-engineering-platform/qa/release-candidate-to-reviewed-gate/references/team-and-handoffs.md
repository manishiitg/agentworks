# QA release team and handoffs

This package is a Builder proposal. Installation copies a pending setup checklist. It does not run a browser, publish a status, approve a gate, merge a PR, deploy a build, or activate recurrence.

## Route

1. **Journey:** Bind release, SHA, build artifact, environment, approved journey revision, expected assertions, test account and fixtures. Save each attempt separately with diagnostic references and observed result. Never turn a failure into a pass by retrying.
2. **Validate:** Run the package validator on `journey-result/v1` before another Crew consumes it. An explicit Workflow validator step is required; an attachment alone does not execute validation.
3. **Optional flake:** If attempts conflict or a retry appears green, investigate the same test and candidate under the approved isolation and retry policy. Save all attempt IDs and a cause classification with confidence. An unresolved flake blocks a pass.
4. **Gate:** The Release Quality Assistant reads the current required-suite policy and all exact-candidate suite results. It writes a complete matrix and one proposed pass, fail, or needs-review decision. Missing, stale, skipped, blocked, or mismatched results cannot satisfy a required suite.
5. **Review and publication:** The authorized gate owner reviews the exact matrix and exception decisions. Publish only through a separate permitted status action with a provider receipt for the same SHA/build/environment/verdict. A prepared or approved brief is not a published status.

For the fictional fixtures:

    python3 scripts/validate_handoff.py journey examples/journey-result.json
    python3 scripts/validate_handoff.py gate examples/journey-result.json examples/release-quality-brief.json
    python3 scripts/validate_handoff.py flake examples/journey-result.json examples/flake-investigation.json
    python3 scripts/validate_handoff.py gate-with-flake examples/journey-result.json examples/flake-investigation.json examples/flake-needs-review-gate.json
    python3 scripts/validate_handoff.py publication examples/journey-result.json examples/approved-release-quality-brief.json examples/status-publication.json

The fixtures show contract shape; they do not prove a real test, approval, or status delivery.

## Exact joins and result rules

Every artifact carries release ID, SHA, immutable build ID, and environment. Journey and gate also bind the journey revision and policy revision. The validator rejects mismatches and validates declared suite coverage. Builder must still read the authoritative CI, browser, and release records to establish that the source IDs exist and are fresh.

A pass needs every required suite to have a passing exact-candidate result with its required evidence and no unresolved flake for the candidate. Fail preserves a confirmed failing required suite. Needs-review covers missing evidence, blocked work, unresolved flake, or a policy exception pending owner decision. A waiver cannot be manufactured by the agent; it needs the configured authority and source receipt.

## Repeat and status rules

Use release ID, SHA, build ID, environment, policy revision, and run ID as stable deduplication keys. On a new candidate, re-enumerate suites and do not reuse another build's pass. Retain failure and retry records, including quarantined tests. Before publishing, re-read current candidate and prior status to avoid a duplicate or stale verdict. Provider publication is a separate artifact joined to the exact gate and owner approval.
