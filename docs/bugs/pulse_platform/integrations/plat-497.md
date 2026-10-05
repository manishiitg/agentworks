# PLAT-497: RTS gateway blocks the incoming Gmail setup review link

State: fixed on main; gateway deployment and live consent still required.
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

## Remaining

Deploy the updated gateway on RTS, then verify a fresh browser review link,
Google consent and receiving activation. A backend restart invalidates pending
plans; ask Builder to prepare a new link after deployment. Do not classify
successful plan preparation as verified Cloud provisioning or real delivery.
