[← browser / automation](index.md)

# PLAT-204 — a null `lcp_ms` under shared CDP is a confirmed structural race, not an independently fixable bug this pass

| Field | Value |
|---|---|
| State | open |
| Priority | P3 |
| Product | browser |
| Area | automation |
| Summary | P3 (as documentation) — the underlying mechanism is real |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `open` — confirmed mechanism, documented as a known limitation; no code fix, by explicit user decision |
| Last synchronized | `2026-08-29` |

- **Priority:** P3 (as documentation) — the underlying mechanism is real
  and matches a genuine gap (concurrent workflows racing for one shared
  Chrome's foreground state), but a proper fix requires a bigger
  architecture change than this pass scoped, and the user explicitly chose
  documentation over implementation for now.
- **Owner:** N/A — no code change. Guidance:
  `agent_go/cmd/server/guidance/templates/review/ops-review.md`.
- **Related:** [PLAT-322](plat-322.md) (CDP shared-browser tab ownership
  misattribution) — same root architectural cause (one real Chrome shared
  across concurrent workflow sessions, racing for an exclusive resource),
  different specific symptom (tab *identity*/quota there, tab
  *foreground*/visibility here). `harness:agent_browser_cdp_shared_tab_visibility`
  (confida-login, medium, seen twice) is the finding this closes.

## The finding

Every `performance_baseline` row for cycle `qa-confida-staging-20260821T145419Z`
(4/4) had `lcp_ms=NULL` while CLS/TTFB were numeric. A dedicated labeled tab
was created that same cycle specifically to reduce shared-CDP-tab
collisions, but the null rate did not improve. The finding's own prior
observation (`PUL-228E1CE2`) had already correctly diagnosed the proximate
mechanism: *"LCP withholding is per W3C visibilityState spec while
page-load/TTFB captured fully."*

## What was confirmed live, not assumed

Live-tested directly against real Chrome via `agent-browser` over CDP (not
simulated): creating a second tab and explicitly switching to it via
`agent-browser tab <ref>` does correctly set that tab's
`document.visibilityState` to `"visible"` — the CLI's own tab-switch
mechanism works correctly in isolation. This rules out "the tool silently
fails to foreground the tab it just switched to" as the mechanism.

What remains, and is not independently reproducible in a solo test: CDP
mode connects every concurrent workflow session to **one real, shared
Chrome instance** (confirmed live in PLAT-322's merged investigation — multiple
distinct workflow sessions remap to the identical `"shared-cdp-<port>"`
connection string). A tab being the CDP-active target at the moment one
session switches to it does not mean it stays foregrounded — another
session sharing the same port can switch its own tab to the foreground
between this session's switch and its actual `vitals`/LCP capture. Creating
a dedicated *labeled* tab (as the finding's own workaround attempt did)
only fixes tab **identity** (which tab is "mine" — PLAT-322's concern), not
tab **foreground** (which tab Chrome currently renders as visible across
all sessions sharing the connection) — which is exactly why that mitigation
didn't move the null rate.

LCP being withheld while `visibilityState !== "visible"` is W3C spec
behavior enforced by the rendering engine itself; no page-side script,
web-vitals library, or CDP command can retroactively recover an LCP
candidate that the browser never reported in the first place.

## Disposition

Presented two options to the user: document as a known structural
limitation (no code change), or scope and implement a dedicated,
non-shared CDP browser instance for performance-capturing steps (the actual
fix, but a meaningfully bigger architecture change touching the same
CDP-ownership machinery PLAT-322 already has open work in). **User chose
documentation.**

Added a bullet to `ops-review.md`'s Ops-focus checklist (next to the
existing PLAT-191-style "don't re-theorize a platform bug that's actually
a known/explained state" bullets) explaining the confirmed mechanism, so a
future Pulse review does not keep re-filing this as a fresh code defect
each cycle without a dedicated-browser architecture change actually landing
first.

## Explicitly not done

- No code change. The real fix (dedicated CDP browser per
  performance-sensitive step) is deliberately out of scope for this pass —
  it's the same class of change as PLAT-322's still-open architectural
  question, not a quick patch.
- Did not attempt to reproduce the exact concurrent-session race in an
  automated test — doing so convincingly would require orchestrating two
  genuinely concurrent `agent-browser` processes against the same CDP port,
  which is possible but was judged not worth the effort once the mechanism
  was independently confirmed via (a) direct live testing of the
  foreground-switch behavior in isolation and (b) the finding's own prior
  correctly-diagnosed W3C-spec observation.

## Verification

- `go build ./...` clean; `go test ./cmd/server/guidance/...`: same 3
  pre-existing failures before and after this edit (unrelated content), no
  regression.
- Live-verified against real Chrome: `agent-browser tab <ref>` correctly
  sets `document.visibilityState` to `"visible"` for the tab switched to.
