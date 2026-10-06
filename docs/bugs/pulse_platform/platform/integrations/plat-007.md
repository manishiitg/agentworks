[← platform / integrations](index.md)

# PLAT-007 — image verification cannot reliably read workflow images

| Field | Value |
|---|---|
| State | closed |
| Priority | P1 |
| Product | platform |
| Area | integrations |
| Summary | media tool path normalization and model selection |

| Coordination | Value |
|---|---|
| Assigned agent | `Claude Code` |
| Ticket state | `runtime_e2e_reverify` |
| Last synchronized | `2026-08-04` |

> Claim this ticket in this file before implementation. During active work,
> update this fragment rather than the shared index; synchronize the index
> once at handoff, review, or completion.


- **Priority:** P1
- **Owner:** media tool path normalization and model selection
- **Legacy source:** Instagram `route-build-carousel`, currently
  `awaiting_verification`
- **Problem:** `read_image` rejected existing absolute workflow paths as
  relative because it expected a `_users/default/...` layout. Earlier runtime
  evidence also showed a retired default vision model returning 404.
- **Impact:** image-producing workflows can pass only renderer provenance and
  hashes, not direct visual/OCR verification.
- **Current state:** implementation exists for absolute workspace-path
  normalization, rejection of relative/out-of-workspace paths, and dynamic
  provider/model discovery via `list_llm_capabilities`. The focused workspace
  unit tests pass. This item is not awaiting implementation; it is awaiting a
  rebuilt-runtime E2E against an actual workflow image so the linked finding
  can be verified and closed.
- **Acceptance:** an E2E creates an image under a real workflow execution
  folder, reads it by the exact absolute and workflow-qualified paths, and uses
  a supported configured model. A bad path and unavailable model produce
  distinct actionable errors.
- **Related recurrence:** [PLAT-328](../../goals/step-execution/plat-328.md) records the
  same absolute-versus-canonical workspace-path mismatch in the trusted
  deterministic-routing reader. It is tracked separately because PLAT-007's
  media-tool normalization did not cover that internal read path.
