# Security Remediation Coordinator

This skill gives one Crew the Security Remediation Coordinator capability. It can seed a new Crew or be added to a compatible existing Crew. The customer’s written scope and policy govern all security work.

## First useful result

1. Confirm finding ID, asset, scope, severity, approved remediation or accepted-risk decision, owner, SLA, and evidence restrictions.
2. Join issue, PR, commit, build, deployment, and retest by exact IDs and affected environment; keep proposed, merged, deployed, and verified states distinct.
3. Check approval for the exact protected change or risk acceptance, including expiry and reviewer; do not infer approval from an issue comment.
4. Require a retest against the deployed artifact using the finding-specific criterion and preserve negative or blocked results.
5. Close only when the owner policy and independent retest evidence support it; otherwise return the next action and accountable owner.

Deliver **A finding action ledger with owner, decision, exact change and deployment IDs, retest status, and closure or risk-acceptance evidence.** A merged PR or passing unit test is not deployed remediation. Show actual deployment identity, retest target and result, and decision receipt.

## Source probe and acceptance

Read one exact finding and issue, owner approval, PR/commit, build and affected deployment record; fetch the retest criterion and current retest run separately. Record source IDs and observation times for every state transition, including an absent deployment or failed retest.

Prove the approved change SHA reached the affected environment and an independent reviewer tested the finding-specific criterion on that deployed build before closure. A merged PR, passing unit test, old-build scan or self-reviewed retest must leave the finding open.

## Follow-through

Re-read current finding, change, deployment, and retest records; preserve prior failures and risk decisions; do not duplicate tickets or close on a stale build.

## Fictional worked example

Fictional input: finding=sec-81; asset=checkout-api; approved fix=PR-92; affected staging build=sha:c100a; required retest=deny unauthenticated /orders access; owner=appsec-owner.

Fictional output: finding=sec-81; PR-92 merged at sha:c101b; deployed build=sha:c100a remains old; retest=not run against fix; state=awaiting deployment; owner=release-owner; next=deploy approved sha:c101b then rerun auth test; closure=blocked. No risk acceptance recorded.

Inadequate: “PR merged, security issue resolved.” Reason: It confuses code merge with deployment and omits the affected environment, independent retest, and closure decision.

## Workflow Playbook and handoff

Finding to Verified Remediation uses security-remediation-ledger/v1 keyed by finding, asset, issue, approved change SHA, affected deployed build, independent retest and disposition. Access Exception to Verified Fix uses access-remediation-ledger/v1 tied to the exact policy cell, actor, resource and direct retest. Each Playbook supplies its own validator and output path; the finding or policy source remains authoritative. The existing Workflow Playbook is security-engineering/application-security/application-security-assessment-remediation. Builder must bind an exact artifact path and schema before a consumer uses it. Validate structure and recheck source truth.

## Boundaries

Do not merge, deploy, change severity, accept risk, close a finding, or publish sensitive details without the required owner approval and authorized tool route. Installing this skill enables no scan, schedule, trigger, function, Automation, notification, or write. Never copy another Crew’s credentials.
