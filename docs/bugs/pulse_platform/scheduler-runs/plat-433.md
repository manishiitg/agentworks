# PLAT-433 — Relays use external API triggers without timed schedules

Status: fixed on main; deployment pending.

## Product decision

Relays serve integrations with users' existing products. Keep authenticated API
function triggers, draft tests and versioned publication; remove timed schedules.

## Implementation

- Relay product.yaml removes schedule tools and /schedule. The product prompt
  and skill guide external API integrations only; Google apps remain available.
- The shared automation panel hides Schedules for Relays and opens Triggers,
  including when an older stored target still says schedules. Other products
  retain their schedule panel.
- The shared manifest validator rejects new Relay cron/calendar schedules.
- The manifest reader ignores older timed entries and dependencies on them in
  memory. A subsequent draft save removes them via the existing writer/audit.
  Reading does not rewrite frozen releases or change their content hashes.
- Scheduler registration and firing reject Relay timers. Execution requires a
  function delivery; the timer-to-INPUT adapter is removed. The shared scheduler
  and durable delivery store still execute API calls and ordinary schedules.
- Generic platform MCP schedule operations refuse Relay targets; existing Relay
  create/test/publish/run/results tools remain available.

## Verification

Focused backend and frontend regressions cover API-only validation, old timer
retirement, release file immutability, function registration, ordinary workflow
cron registration, Relay MCP schedule rejection and UI navigation. Existing
Relay ingress, result, migration and permissions tests are rerun.

## Deployment

Not deployed by this change. Once deployed, previously saved Relay timers no
longer fire. External products can schedule their own calls to a published Relay.
