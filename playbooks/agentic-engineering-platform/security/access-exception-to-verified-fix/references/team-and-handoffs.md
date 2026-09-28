# Access exception to verified fix

## Route

1. Builder binds a written scope, versioned permission policy, isolated actor and resource fixture, exact build/environment, evidence handling and named Security, asset, change, independent retest and closure owners. Inspect existing Crew capabilities first.
2. Access Review Analyst enumerates expected cells before testing. It records a selected mismatch only after observing the same authorized actor, tenant, resource and action through a direct server request. UI absence is supporting evidence, never a server denial. `access-review-matrix/v1` carries policy, attempt, build, exact selected cell and source references. An ambiguous policy or missing direct result remains unresolved.
3. A blocking `matrix` validator checks the artifact before Security Remediation Coordinator receives it. The coordinator re-reads current policy and issue state, proposes or records an approved change, and joins reviewed source SHA to an actual deployment in the affected environment. Approval or merge alone is not a fix.
4. Retest the exact selected cell against the deployed build using an authorized independent reviewer and a distinct attempt. Run the blocking `remediation` validator on both artifacts. Close only after a passing same-cell retest, owner closure approval and a closure source record. A failed, blocked, stale or different-cell retest keeps the case open.

## Evidence and repeat

Stable identity includes tenant, scope, policy revision, actor role/tenant, resource type/tenant/ID, action, environment and original build. Save policy and source IDs, redacted trace, issue, approved SHA, deployment, retest, closure and run IDs. A later run re-reads policy, issue and deployment first. New policy or fixture identity creates a new comparison; do not overwrite a failed attempt or duplicate an issue. Manual-only is a valid activation decision.

## Example acceptance

The fictional case expects a tenant A member to receive 403 when reading tenant B invoice 7. A direct request on build `sha:b912e` returns 200; UI link absence does not erase the mismatch. The pending ledger waits for approval and deployment. The verified ledger cites approved `sha:b913f`, deployment `dep-17` and independent same-cell denial on that deployed build. The invalid ledger claims closure after a merge with no deployment or retest. Fixtures are structural examples, not permission to test a real tenant.
