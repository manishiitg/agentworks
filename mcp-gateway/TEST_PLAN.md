# CapLayer MCP Gateway local test plan

## Goal and test setup

Prove that a local admin can connect real MCP servers, discover and review their tools, grant one tool to an MCP client, call it through the gateway, resync it, and inspect the audit trail. Exercise the embedded CapLayer console in the Codex in-app browser as well as the standalone admin API. Use this branch, a loopback gateway, and a local frontend. Never use production credentials or mutate external accounts during the live tests.

Live upstreams: Context7 is the full read-only call target. DeepWiki and Microsoft Learn are discovery/connectivity targets if available; an upstream outage is recorded separately from a gateway failure. A deterministic in-process MCP server covers denials, revocation, PII, deletion, and races.

## Cases and expected results

| ID | Area | Test | Expected result |
| --- | --- | --- | --- |
| A1 | Build | `go test -race ./...`, `go vet ./...`, frontend typecheck and gateway UI tests | All pass. |
| A2 | Gateway OAuth | In-process MCP client completes OAuth and calls approved tool | Client sees only its granted tools; allowed call succeeds. |
| A3 | Live MCP | Context7 discovery, approval, grant, `tools/list`, `resolve-library-id`, audit | Both Context7 tools discovered; only granted tool listed; call succeeds; allow/OK audit event recorded. |
| A4 | Other MCPs | Connect and discover DeepWiki and Microsoft Learn | Valid reachable servers expose tool snapshots; failures include the upstream reason. |
| B1 | Browser startup | Open embedded CapLayer on this branch | Servers, Tools, Groups, Users, Audit, PII and Connect sections render. |
| B2 | Browser auth | Open console without token, then sign in with local admin token | Unauthorized request prompts for token; correct token loads data; no loopback bypass. |
| B3 | Browser connect | Add Context7 from catalog, inspect server and tool list | Server shows connected; discovered tools appear individually as pending review. |
| B4 | Browser review | Approve a Context7 tool and resync | Approved status persists through resync if schema is unchanged. |
| B5 | Browser grants | Grant one approved tool to a local user or group | Only that tool becomes visible to that principal. |
| B6 | Browser audit | Make a call and inspect Audit | Decision, outcome, identity, tool and timestamp are shown without raw arguments. |
| C1 | Default deny | Try an unapproved or ungranted tool | Hidden from `tools/list`; direct call denied and audited. |
| C2 | Revocation | Revoke a grant and call again | Next call is denied. |
| C3 | Schema changes | Change upstream tool schema, then resync | Tool returns to pending review; old approval cannot authorize it. |
| C4 | Deletion | Delete a connector and recreate its namespace | Its tools, grants and PII rules do not carry over. |
| C5 | PII | Exercise regex mask, block and input review rules | Policy acts before upstream call; review requires explicit approval; denied calls are audited. |
| C6 | Security | Probe admin without token, unsafe URL/query, private egress, public bind | Each fails closed by default. OAuth-only catalog servers cannot be added as working connectors. |
| C7 | Stability | Concurrent resync/remove; bounded audit and review queues | No leaked active connector; queues stay within limits. |
| C8 | Pagination | Upstream returns multiple `tools/list` pages | All pages are discovered before the gateway disables missing tools. |
| D1 | Restart | Restart local gateway | Document current alpha limit: governance data in memory is lost; do not use for a shared deployment. |

## Execution record

Run date: 2026-09-28. Branch: `fix/gateway-review-1`, based on `8657c197c` plus the test changes in this document. Local gateway: `http://127.0.0.1:18746`; local frontend: `http://127.0.0.1:51734`. Frontend uses an isolated runtime config in `/tmp/caplayer-pr228-runtime.js`; the tracked `frontend/public/runtime-config.js` is untouched. The gateway uses `/tmp/caplayer-pr228-local`. These are local test instances, separate from the pre-existing server on port 18745.

Record an upstream outage as blocked, not passed. Repair product failures and rerun their case plus the relevant regression suite.

| ID | Result | Evidence / notes |
| --- | --- | --- |
| A1 | Pass | `go test -race ./...` and `go vet ./...` in `mcp-gateway`; `go test ./...` and `go vet ./...` in `agent_go/pkg/mcpoauth`; frontend `tsc -b` and 52 gateway/switcher UI tests. |
| A2 | Pass | `TestM0GovernedCallPath` completed OAuth, hid ungranted tools, allowed a granted call and denied it immediately after revocation. |
| A3 | Pass | `GATEWAY_LIVE_TEST=1 go test ./e2e -run TestM0LiveContext7 -v -count=1`: discovered two tools, approved and granted one, called `resolve-library-id`, and recorded allow/OK audit. The test previously omitted approval; that test setup is fixed in this branch. |
| A4 | Pass | Added Context7, DeepWiki and Microsoft Learn through the running gateway's catalog API: HTTP 201 for all three, eight quarantined tools discovered (2 + 3 + 3). |
| B1-B6 | Blocked | The Codex in-app browser rejected reopening its crashed tab with a browser URL policy error. No visual browser pass is claimed. The local frontend remains running on port 51734 for manual inspection. API paths behind B2-B6 were exercised below, and frontend component tests passed. |
| C1 | Pass | Local group API key with a grant to a quarantined Context7 tool saw `tools/list=[]`; direct call returned `denied: tool is not active`. |
| C2 | Pass | After exact-version approval and resync, that client listed only `context7__resolve-library-id` and called it successfully. Revoking its group grant made the next list empty and the next call return `denied: no grant for tool`. Three decision/outcome audit events were present. |
| C3 | Pass | `TestSchemaChangeQuarantinesTool` and `TestResyncDisablesRemovedTools` passed under the gateway race suite. |
| C4 | Pass | Running gateway API deletion removed DeepWiki tool grants, group-server attachment and a scoped PII rule. Re-adding the same provider did not restore the grant. Store cascade tests passed. |
| C5 | Pass | Running gateway PII sample endpoint masked `alice@example.com` and blocked a valid test SSN. `TestPIIGuardsCallAndReviewRetry` covered input review, one-use approval, output blocking, and audit redaction. |
| C6 | Pass | Admin without token returned 401. Custom URLs with query credentials or loopback HTTP returned 400; OAuth-only Notion returned 400. Public bind and public URL startup probes exited with explicit errors. Consent page returned `frame-ancestors 'none'` and `X-Frame-Options: DENY`. |
| C7 | Pass | `TestConcurrentResyncAndDeleteCannotRestoreConnector` ran ten races under `-race`; all deleted connectors stayed gone. A slow resync no longer blocked deletion of another connector. Ring-buffer audit and per-caller review-capacity tests passed. |
| C8 | Pass | `TestPaginatedUpstreamKeepsAllApprovedToolsOnResync` used a real in-process MCP server with page size one and three tools. Gateway discovery found all three, and resync kept all three approved. |
| D1 | Observed limit | Before restart: 3 connectors, 8 tools, 1 group, 3 audit events. After restart: all four counts were 0 and the old group key returned 401. Reconnected Context7, DeepWiki and Microsoft Learn for local inspection; eight tools are present and the Context7 resolver is approved. |

## Browser walkthrough still to perform

The in-app browser could not be automated in this run. When its browser policy permits the local page, keep the local gateway and frontend running and follow these steps:

1. Open `http://127.0.0.1:51734/`. The isolated runtime config selects CapLayer. Expect a token prompt, not gateway data, on the first unauthenticated load.
2. Read the local test token from `/tmp/caplayer-pr228-local/admin-token` and enter it. Expect the Servers section. Do not copy the token into the test report.
3. Under connected servers, expand Context7, DeepWiki and Microsoft Learn. Expect 2, 3 and 3 discovered tools respectively. Context7 `resolve-library-id` is approved; the others are pending review.
4. Open each CapLayer section (Servers, Tools, Groups, Users, Audit, PII and Connect). Confirm loading states finish, counts match the API, and no section shows a blank or broken panel.
5. Add one new catalog server if desired, expand it, approve one tool, resync, and confirm its approval survives. Avoid OAuth-only providers; they should say that upstream OAuth is not supported yet.
6. In Connect, select “Send test request.” Expect an OAuth challenge and a reachable result. In PII, test `alice@example.com` and a test SSN; expect mask and block. Confirm Audit labels decisions and outcomes without showing tool arguments.

The Codex browser tool rejected reopening its pre-existing crashed tab with: “The browser URL policy blocks this action. The requested URL protocol is not allowed.” No workaround or alternate browser surface was used.

## Release boundary

This plan validates a single-user loopback alpha. Team internet testing needs individual sign-in and durable governance storage. A separate reverse proxy can expose a loopback listener without the gateway knowing, so none is used in this run.

## Full pre-merge pass (2026-09-28, PR #228 at `9c262770c`)

| Scope | Result | Evidence |
| --- | --- | --- |
| Gateway | Pass | `go test -race ./...` and `go vet ./...` in `mcp-gateway`. Includes OAuth, grants, denial, revocation, PII, audit, connector races, and paginated discovery. |
| Live upstream | Pass | `GATEWAY_LIVE_TEST=1 go test ./e2e -run TestM0LiveContext7 -v -count=1`: two Context7 tools discovered; approved resolver call succeeded. |
| OAuth module | Pass | `go test ./...` and `go vet ./...` in `agent_go/pkg/mcpoauth`. |
| Frontend | Pass | Full Vitest run: 389 files and 2,192 tests passed, one skipped. `npm run build` passed all catalog checks, TypeScript, Vite build, release assets, and enforced bundle budget. Eager JS was 959.29 kB gzip, above the 950 kB warning threshold and below the 1,030 kB limit. |
| Agent Go PR paths | Pass | The exact CI topology matrix and focused OAuth/external MCP server tests passed locally. The nested `mcpbridge` build now uses a temporary workspace with the resolved provider dependency; the corresponding GitHub deterministic-contract job passed after this fix. |
| Agent Go broad suite | Failed outside changed paths | `go test ./... -timeout 5m` failed in unchanged server catalog, provider-key, and prompt-size tests, unchanged browser anonymous-capture test, and unchanged workflow model-default test. The provider checkout returns Claude Opus 5.5 and GPT-6 Sol builder defaults while the test expects Sonnet 5 and GPT-6 Astra. These failures are not counted as gateway passes. |
| Agent Go vet | Failed outside changed paths | `go vet ./...` reports a missing `cancel()` on one path in unchanged `message_sequence_stop_test.go`. |
| Browser visual walkthrough | Blocked | The Codex in-app browser previously refused reopening its crashed local tab under URL policy. B1-B6 remain unverified visually; API and component tests cover their underlying paths. |

The broad-suite failures and browser gap mean this is not a clean full-product test pass. Keep PR #228 unmerged until the desired merge gate is clear and the visual walkthrough has been completed.
