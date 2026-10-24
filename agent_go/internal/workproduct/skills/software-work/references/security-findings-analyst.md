# Security Findings Analyst

This skill gives one Crew the Security Findings Analyst capability. It can seed a new Crew or be added to a compatible existing Crew. The customer’s written scope and policy govern all security work.

## First useful result

1. Confirm written target, environment, time window, allowed methods, rate and side-effect limits, stop conditions, and evidence access before any active check.
2. Bind each observation to an exact asset, source revision or deployed build, finding ID, detector, timestamp, and affected component.
3. Validate applicability and reproducibility with permitted evidence. Label scanner-only or stale reports unconfirmed rather than treating them as vulnerabilities.
4. Apply the customer severity and asset-criticality policy; distinguish technical impact, exposure, confidence, and business priority.
5. Return an owner decision queue with bounded remediation options, risk, due date, verification criteria, and restricted evidence reference.

Deliver **A finding queue with exact asset and build, source and reproducibility evidence, confidence, customer severity, owner, and proposed verification rule.** A scanner severity is an input, not a verified business risk; retain source IDs, scope proof, confidence, and missing checks.

## Source probe and acceptance

Read the written authorization, exact in-scope asset/environment/build, one detector finding and its evidence. If applicability depends on deployed code or package version, join to the deployed artifact or SBOM; a scanner title and repository dependency alone are insufficient.

Reproduce one applicability and customer-severity decision from scope, deployed evidence and policy. Keep a scanner-only or stale result unconfirmed; withhold confirmed severity, remediation deadline and closure claims until the required source and owner decision exist.

## Follow-through

On another run, reconcile stable finding and asset IDs, recheck version and deployment state, carry unresolved evidence forward, and avoid duplicate tickets or false closure.

## Fictional worked example

Fictional input: written scope=staging api.example.test only; finding=scan-44; asset=checkout-api; build=sha:a55c1; report=outdated dependency; severity policy=appsec-v2; owner=security-lead.

Fictional output: finding=scan-44; asset=checkout-api; build=sha:a55c1; source=dependency-report:run-18; observed package version=2.3.1; deployed applicability=unknown pending SBOM confirmation; confidence=medium; customer severity=pending; owner=checkout-owner; proposed next=verify deployed SBOM then review fixed version; closure proof=deployed artifact and repeat scan.

Inadequate: “Critical vulnerability found; patch immediately.” Reason: It assumes scanner output is confirmed, omits the actual deployed version and customer severity rule, and gives no owner or retest criterion.

## Workflow Playbook and handoff

Finding to Verified Remediation consumes security-finding/v1 keyed by asset, environment, build, finding ID, evidence, confidence, severity rule, owner, and retest criterion. Application Security Assessment and Remediation remains a method guide. A remediation Crew must recheck scope and current status. The existing Workflow Playbook is security-engineering/application-security/application-security-assessment-remediation. Builder must bind an exact artifact path and schema before a consumer uses it. Validate structure and recheck source truth.

## Boundaries

Do not test outside written scope, run intrusive probes without permission, expose exploit details or secrets, accept risk, or change code without an authorized review route. Installing this skill enables no scan, schedule, trigger, function, Automation, notification, or write. Never copy another Crew’s credentials.
