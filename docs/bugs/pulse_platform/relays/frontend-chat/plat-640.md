[← relays / frontend-chat](index.md)

# PLAT-640: Render Relay graphs from comments in relay.py

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | relays |
| Area | frontend-chat |
| Summary | Show a read-only Relay graph from structured source comments, without a separately maintained relay.md. |

## Decision

The user rejected the separate Markdown overview in PLAT-637. Builder chat now
maintains standalone `# @relay node {...}` and `# @relay edge {...}` JSON comments
beside the Python logic. Python remains the execution definition; graph comments
do not gate running or publishing.

## Fix

- Default to Graph; retain Runs and optional Code. Reuse React Flow, Dagre and
  shared route colours. Draw skip branches using Dagre's routed points.
- Display input, agent, script, decision and output nodes with labelled edges.
  Selecting a node shows annotated prompts, message sequence, model, tools,
  skills/MCP and input/output descriptions.
- Match named agent calls to recorded status, output and tool receipts. Published
  run graphs read frozen release source. Draft tests explicitly show the current
  draft. Scripts, decisions and branch edges have no invented runtime status.
- Parse standalone JSON comments outside quoted strings/docstrings; report source
  lines for invalid records, duplicate IDs or dangling edges. Do not execute Python.
- Stop creating, reading or requiring relay.md. Preserve any existing user files.
  Refresh graphs through the existing relay.py live plan notices.
- Update product.yaml command guidance, product-owned Builder prompt, relay-builder
  skill and design/decisions documentation. No new executor or tool engine.
- Browser testing exposed an unstable React Flow selection callback and zero-height
  canvas. Stabilize selection/empty calls and explicitly size the canvas.

## Verification

- Nine focused frontend checks passed: parser, default Graph, source/code access,
  missing annotations, frozen release graph/receipts, onboarding and shared layout.
- Frontend TypeScript build passed.
- Relay product, livefeed and real workspace/Python tests passed. The real test
  creates an annotated starter without relay.md, publishes immutable versions,
  creates/lists triggers and runs Python to return hello Ada.
- In-app browser tested the actual components using labelled sample data: branched
  graph, node selection, prompt/sequence/tools, v1 frozen label, completed calls,
  output and lookup_customer receipt. The preview API rejected any relay.md read.
  Screenshot: /tmp/relay-comment-graph-20261007.jpg.

## Left

Deploy to the hosted products. Existing source without annotations remains runnable;
Builder chat can add graph comments while preserving its Python behaviour.
