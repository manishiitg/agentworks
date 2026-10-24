# Access Review Analyst

This skill gives one Crew the Access Review Analyst capability. It can seed a new Crew or be added to a compatible existing Crew. The customer’s written scope and policy govern all security work.

## First useful result

1. Confirm approved policy source, actor roles, tenant and ownership states, target build, safe fixtures, and written scope.
2. Enumerate expected allowed and denied cells before testing; use isolated actors and resources so results do not leak customer data.
3. For each selected cell, capture UI behavior and server-observable result where applicable, including direct navigation and cross-tenant requests.
4. Report mismatches, untested cells, policy ambiguity, and evidence gaps separately; a hidden button alone does not prove denial.
5. Prepare an owner-reviewed exception or policy clarification and a same-cell retest rule.

Deliver **An actor-by-resource-by-action matrix with expected and observed access, direct-route and cross-tenant evidence, exceptions, and an accountable owner.** Every claimed permission result needs actor, role, tenant, resource, action, expected policy, observed status, build, and source evidence.

## Source probe and acceptance

Read the approved role/tenant policy revision, isolated actor and resource fixture, exact build, and one expected allow or deny cell. Capture both UI state and a direct server attempt with actor, tenant, resource, action, HTTP result, trace ID and observation time.

Verify the observed direct response against the predeclared policy cell. A hidden UI link does not prove a server denial; an unknown or blocked response is untested. For a cross-tenant exception, retain the same safe actor/resource cell for a later deployed retest.

## Follow-through

Preserve earlier matrix attempts, compare policy and build revisions, and re-execute the same denied cells after an approved fix before closing an exception.

## Fictional worked example

Fictional input: policy=roles-v4; build=sha:b912e; staging tenant A/member and tenant B/resource-7; expected=deny tenant A member read of tenant B invoice; test actor=test-member-a.

Fictional output: cell=roles-v4/member/tenant-b-invoice/read; build=sha:b912e; actor=test-member-a; expected=403; UI=invoice link absent; direct API GET /invoices/7 observed=200 with tenant B fields; verdict=fail; evidence=trace:access-07 redacted; owner=authorization-team; retest=same cell after deployed fix.

Inadequate: “The invoice link is hidden, so tenant isolation works.” Reason: It skips direct server denial, exact policy and build identity, and the cross-tenant data observation.

## Workflow Playbook and handoff

Access Exception to Verified Fix consumes access-review-matrix/v1 keyed by policy revision, build, environment, actor role and tenant, resource and tenant, action, expected/observed direct result, attempt ID, and evidence. Role and Permission Validation remains the test-method guide. Security remediation receives only validated, owner-reviewed exceptions. The existing Workflow Playbook is browser-qa/role-permission-validation. Builder must bind an exact artifact path and schema before a consumer uses it. Validate structure and recheck source truth.

## Boundaries

Do not use real users or production identities without authorization, broaden privileges, make destructive privileged changes, or infer policy from current UI behavior. Installing this skill enables no scan, schedule, trigger, function, Automation, notification, or write. Never copy another Crew’s credentials.
