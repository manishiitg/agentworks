# Security finding team and handoffs

This package is a Builder proposal. Installation copies guidance and a pending setup checklist. It does not scan, create a ticket, change code, deploy, retest, accept risk, close a finding, or enable recurrence.

## Route

1. **Scope and finding:** Verify written target, environment, allowed methods, time window, stop conditions and restricted evidence handling. Read one authorized finding and affected asset/build. Mark scanner-only applicability unconfirmed until the configured verification rule is met.
2. **Validate:** Security Findings Analyst emits `security-finding/v1`. Insert an explicit validator step before another Crew consumes it; an attachment alone does not run validation.
3. **Remediate:** Security Remediation Coordinator re-reads the finding and traces issue → reviewed change → exact built artifact → deployment in the affected environment. Record owner, decision, and approval sources. A merged PR is not deployed remediation.
4. **Retest:** An independent authorized reviewer applies the finding-specific criterion to the deployed artifact. Record exact retest run, target build, result and source; preserve negative or blocked results.
5. **Disposition:** Close only when verified deployment and independent retest support the policy and closure owner has approved the decision. Risk acceptance is a separate explicit decision with authority, scope and expiry; it is not a passing retest.

For the fictional fixtures:

    python3 scripts/validate_handoff.py finding examples/security-finding.json
    python3 scripts/validate_handoff.py remediation examples/security-finding.json examples/security-remediation-ledger.json
    python3 scripts/validate_handoff.py remediation examples/security-finding.json examples/verified-security-remediation-ledger.json

The fixtures demonstrate contract structure, not actual permission, deployed state, retest or closure.

## Stable joins and evidence

Every artifact carries tenant, finding, asset and affected environment. The finding binds the observed build, policy and written scope reference; the ledger retains them and separately names any later deployed build. Never join by a vulnerability title, package name or free-form asset label alone. Builder must read authoritative finding, change, deployment and retest records to establish that source IDs exist and are current. The validator checks artifact structure and internal joins, not the truth of external systems.

An independent retest cannot be inferred from a scanner schedule, CI green state or developer comment. It must identify the affected deployed build, criterion, outcome, reviewer and source record. A valid closure joins the same finding to that retest and a recorded owner approval. Reopening or a new affected build supersedes prior closure. Retain earlier failing attempts and risk decisions.

## Repeat and authority

Use tenant, finding, asset and environment as stable case keys. Re-read current status, existing issues, approvals, deployment and retest before a repeat; do not create duplicate tickets or notifications. A new finding revision or asset build calls for a new applicability check. Keep source access, active testing, change rights and closure rights separate unless the customer explicitly combines them under a reviewed policy.
