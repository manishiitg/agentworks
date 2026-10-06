[← coding-agents / accounts](index.md)

# PLAT-625: Code rejects an admitted private Claude login without a deployment token

| Field | Value |
|---|---|
| State | deployed |
| Priority | P1 |
| Product | coding-agents |
| Area | accounts |
| Summary | Code accepted a private Claude account, then a deployment-token preflight incorrectly refused its signed-in CLI login. |

## What happened

Vaibhav's private Claude account `Void` is signed in on Excellence. The real
private-account CLI verification completed successfully, but his Hosati Code
message returned HTTP 400: `No Claude Code token configured for this deployment`.
The profile has no project/deployment token because its credential comes from
the selected account later in the runtime resolver. The early account admission
succeeded; the legacy token check discarded that account and required a global
or project token instead. This is a backend failure, not stale UI.

## Fix

Reuse the account returned by the existing per-turn admission. When a product
requires explicit Claude authentication, accept that account's stored OAuth
token or its isolated CLI login file. Check legacy credential links before
reading the account HOME. Never probe the service login, relax account admission,
or layer private credentials/environment into default/delegated provider keys.
An unsigned private account tells its owner to sign in in Providers.

## Validation

The focused regression drives the encrypted account registry and principal
admission into the exact query preflight: private login and token accepted;
empty private login with a valid ambient service login, service fallback,
another user's private account and a legacy service credential link rejected.
A second regression drives the real Code HTTP query handler through finalized
agent assembly on that private account with the token gate required; it stops
before the first model request. Existing dedicated-product/multi-user Code
token-policy tests pass. Workspace state is mocked in these local checks; the
separate real private CLI verification passed on Excellence.

## Excellence deployment and verification

Deployed commit `2840c424f` in release
`agents-2840c424-20261006152913`. Public/internal health returned 200; the Linux
slot self-test passed 156 checks, zero failures, 16 skips.

After restart, Vaibhav's private Claude status returned 200, signed in with his
provider identity; its credential file remains independent and mode `0600`.
Ashutosh's request for that private account returned 404. Vaibhav's request for
shared Claude returned 404; admin status confirmed shared Claude still signed
out. No shared fallback was restored. These post-deploy checks verify account
availability/admission; the production Code route is exercised by the local
regression above, which stops before a paid model turn.

## Left

No implementation or deployment work remains. Vaibhav can refresh and retry his
failed Hosati message with `Void` selected. Ankita's independent key-rotation
follow-up remains tracked in [PLAT-623](plat-623.md).
