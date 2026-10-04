# PLAT-427 — Relay authoring and versioned execution through platform MCP

Status: fixed on main; deployment pending.

## Request and implementation

Expose creation, Builder editing, draft testing, publishing, version selection,
and JSON results through the existing platform MCP. Preserve Builder-only Relay
chat and reuse the existing runtime instead of adding a second graph executor.

- Relay product.yaml owns external tool admission alongside its prompt/skills.
- Shared Builder operations retain queued delivery, operation-scoped authority,
  configured model selection, live grant checks, managed plan/file mutations,
  revision checks, edit audit, pending questions and cancellation.
- Explicit relays:write OAuth/PAT permission allows Relay authoring only; default
  connection scopes and selected-workflow AgentWorks Builder rules stay intact.
  Creation requires all-workflow bounds plus account create and product rights.
- Create reserves an identity in private auth storage, seeds object INPUT and a
  function trigger, and writes through the shared workspace/manifest services.
  Retries do not replace an edited Relay; conflicting submissions fail.
- update_relay delegates metadata edits to the existing manifest handler.
- Publishing delegates to the existing snapshot/integrity-checked publisher.
- Draft tests and published calls reuse Relay ingress, function dispatch and the
  scheduler store. Server-owned draft provenance bypasses release selection only
  after authoring authorization. Retry namespaces keep draft and published calls
  separate. Generic MCP Run-session proxies are refused for Relays.
- Polling reuses the existing caller-bound API result reader and artifact links.

## Verification

- Actual Streamable HTTP MCP: scoped catalog, Relay creation, invalid JSON input.
- Creation retry/conflict, explicit consent, creator/viewer and bounded grants.
- Shared Builder operation plus actual update_step prompt edit and publish;
  reader/editor boundaries and Relay consent cannot authorize AgentWorks editing.
- Draft-before-publish dispatch, v1 after v2 publish, retry/input conflict,
  caller-bound polling and reader draft-test denial.
- Ingress tests deliberately stop at executor contract preflight to avoid model
  calls; they verify durable scheduler bindings, not end-to-end model inference.
- OAuth permission validation, token bounds, manifest admission, shared external
  Builder/MCP/access-token regressions; backend build and frontend consent tests.

## Deployment

Not deployed by this change. Enable AGENTWORKS_MCP_BUILDER_ENABLED and explicitly
consent to relays:write with companion read/run scopes. Existing connections do
not gain authoring permission automatically. See docs/relay/README.md.
