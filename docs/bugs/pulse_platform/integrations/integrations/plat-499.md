# PLAT-499: Gmail administrator setup fails at browser review and later provisioning steps

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | integrations |
| Area | integrations |
| Summary | fixed on main, deployment pending. |

State: fixed on main; RTS deployment and human Cloud consent/delivery verification pending.
Priority: P1. Follow-up of [PLAT-483](plat-483.md) and [PLAT-497](plat-497.md).

## Evidence and audit

RTS gateway logs on 2026-10-05 show review GET 200 at 06:56:28 UTC and POST 403
at 06:56:30 UTC, with `Invalid review origin`. The PLAT-497 gateway fix is
therefore deployed and this request reaches the backend. No Google consent or
Cloud mutation has happened in this attempt.

The browser reproduction and code/API audit identify five setup blockers while checking the full
review/callback/provisioning/activation path:

- `Referrer-Policy: no-referrer` makes the review form submit `Origin: null`;
  the origin guard rejects it. This is specified by the
  [Fetch standard](https://fetch.spec.whatwg.org/#append-a-request-origin-header).
- `form-action 'self'` also prevents the form's 303 redirect to a foreign
  consent origin in browsers that enforce this on redirects. Local browser
  requests reach the consent stub only after narrowly allowing that origin.
  See [CSP form navigation](https://www.w3.org/TR/CSP3/#directive-form-action).
- Operation validation rejects dots in Google's normal `operations/acf.…` IDs;
  the previous fake API returned all operations already done and missed this.
  [Google's API enablement example](https://docs.cloud.google.com/cloud-assist/set-up-gemini)
  shows this format.
- Project verification calls Cloud Resource Manager before enabling APIs, but
  OAuth/Gmail projects need not already have that unrelated API enabled.
- A newly created push service account is used immediately. Google documents
  [eventual consistency of 60 seconds or more](https://docs.cloud.google.com/iam/docs/service-accounts-create).

The last three are code/API-contract findings, not claims that live RTS Cloud
provisioning has reached or failed at those stages.

## Fix

The review uses `same-origin` referrer policy and permits only itself and
`https://accounts.google.com` for form navigation. Null and foreign origins
remain rejected; Google callbacks retain `no-referrer`, expiring plans and
single-use PKCE consent. The review capability is not disclosed through a
cross-origin Referer.

Polling accepts dot-bearing operation path segments while excluding traversal,
queries and foreign URLs. Project numbers are resolved read-only from Service
Usage's canonical service name/parent before any mutation, and still compared
with the registered OAuth client's project number. This avoids a Resource
Manager prerequisite; Service Usage must already be available (normally the
Google default). See [the service resource contract](https://docs.cloud.google.com/service-usage/docs/reference/rest/v1/services).

After account creation, setup waits up to 90 seconds for read visibility with
bounded exponential backoff on 404 only. Permission and policy failures still
stop setup immediately. Shared resources, narrow IAM grants, conflict refusal,
private configuration and activation checks remain in force.

## Verification

Browser policy probe and real-handler check with an isolated consent stub: original form POST carries `Origin: null` and returns
403; corrected policy carries the expected origin and returns 303. With the
narrow consent destination allowed, the browser reaches an isolated local
consent stub without sending a Referer. The real Go review handler was also exercised by a
browser through its rendered form: it observed the correct POST Origin and the
consent stub observed no Referer. No Google account is used.

Existing consent/provisioning regressions are extended rather than multiplying
unit tests: opaque-origin rejection, browser-compatible headers, asynchronous
Google operation polling, delayed service-account visibility and no Resource
Manager prerequisite. The full setup/Cloud and gateway regression suites are
checked with the race detector, including wrong-project rejection, admin
revocation, cancellation/replay, permission failure and activation only after
subscription verification.

## Remaining

Deploy this batch on RTS once. Prepare a fresh review link after the backend
restart, complete Google consent as the human administrator, and verify actual
Cloud provisioning, watch readiness and real email delivery. Google project
permissions and organization consent policies require live verification;
passing simulated API tests does not prove those external permissions.

## Register notes

[PLAT-499](plat-499.md), P1, fixed on main, deployment pending. Fixes null review Origin, blocked Google redirect, dotted Google operation IDs, Resource Manager prerequisite and delayed push-account visibility. Real Cloud consent and delivery still require RTS verification.
