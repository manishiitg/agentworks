# PLAT-497: RTS gateway blocks the incoming Gmail setup review link

State: deployed on RTS; review forwarding verified. Later setup blockers and live Cloud verification are tracked in [PLAT-499](plat-499.md).
Priority: P1. Follow-up of [PLAT-483](plat-483.md).

## Live observation

The RTS backend prepares the administrator's plan successfully, but opening its
`/api/gmail-inbound/setup/start?plan_id=...` link returns
`{"error":"authentication_required"}`. Gateway logs on 2026-10-05 at 05:25:16 UTC
show a 401 in zero milliseconds, before the request reaches the plan handler.
The app's authentication middleware already exempts the exact review route;
the deployment gateway's browser/JWT gates were omitted from that change.

## Fix

The gateway forwards only GET/POST on the exact review path, and GET on the two
existing Google callbacks when the state belongs to the `gmail-setup-` flow.
It strips a caller-supplied X-User-ID before proxying. No app JWT or shared
password cookie is required for those browser navigations. The backend retains
expiring plan validation, current administrator checks, one-use consent codes,
PKCE, frozen-resource verification and all provisioning authorization.
Adjacent paths, unsupported methods, route management and sender confirmation
remain authenticated. Other OAuth flows retain their existing gateway behavior.

## Verification

One focused HTTP regression drives a running gateway proxy without browser
credentials in both gateway modes: expired reviews and setup callbacks reach
the upstream's 410; management, neighboring paths and unsupported methods
remain 401, and spoofed identity headers are removed. The full standalone
gateway suite passes with the race detector. A read-only live comparison using
an invalid diagnostic plan confirms the backend returns 410 while the public
RTS URL returns 401; the gateway is the blocker. No Google consent or resources were
changed by these diagnostic checks.

Live follow-up: on 2026-10-05 at 06:56:28 UTC the review GET returns 200;
the POST two seconds later reaches the backend origin guard and returns 403.
This verifies the gateway repair. The review policy and later provisioning
blockers are tracked separately in [PLAT-499](plat-499.md).

## Remaining

No gateway work remains for this issue on RTS. Deploy PLAT-499 and prepare a
fresh plan after restart before continuing human consent. Actual Cloud setup,
mailbox watch readiness and real delivery remain verification work for PLAT-483.
