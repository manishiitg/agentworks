# Ordered Gmail action rules QA

- Evidence: `/tmp/agentworks-gmail-action-qa/project.png`, `workflow.png`,
  `workflow-narrow.png`, `crew-narrow.png`, `crew-light.png`.
- Production shared Incoming email component rendered with sample responses for
  Crew, Code and workflows. No live OAuth, mailbox reads or trigger runs.
- Default viewport 1280×720, full-page captures for both rule cards. Narrow
  right-pane check at 420×800: viewport and document both measure 420px.
  Temporary viewport override reset after testing.
- Ordered cards show rule name/order, sender policy, conditions, saved chat
  instruction or exact route/groups, and Enabled/Paused state. Common filters
  remain separate; first-match behavior is explained. No configuration editors.
- Expanded activity visibly names the matched rule. Existing Ask AI and Fetch
  emails actions remain the Builder/chat entry points; automated UI tests verify
  the setup message includes stable IDs and preservation of untouched rules.
- Dark and light narrow layouts inspected; text wraps without horizontal overflow.
  Existing typography and color tokens retained. No scoped layout findings.
- Backend Gmail/webhook regressions, full inbound package race tests, relevant
  frontend tests and production frontend build passed. Live Gmail remains untested.

final result: passed

---

## Previous QA reports (preserved)

# Gmail sender rules and Google access disclosure QA

- Evidence: `/tmp/agentworks-gmail-rules-qa/collapsed.png`,
  `final-collapsed.png`, `light-collapsed.png`, `narrow-expanded.png`.
- Production components rendered with sample mailbox/account API responses;
  no live OAuth, permission changes, email reads or trigger executions performed.
- Default viewport 1280×720; narrow pane check at 420×800, device scale 1.
  Narrow document and viewport both measure 420px; no horizontal overflow.
- Change access opens from the existing account action. The header closes and
  reopens the form, preserving pending Docs access and showing its unsaved count.
  Native button semantics expose `aria-expanded` and `aria-controls`.
- Sender domains/addresses and alternative phrases display as OR, with separate
  groups described as AND. The configuration stays read-only.
- Fetch emails uses the shared chat action: first click arms confirmation,
  deliberate confirmation sends the mailbox request to the target chat callback.
  UI regression tests also cover the workflow Builder delivery helper.
- Dark and light collapsed states and narrow expanded state inspected. Existing
  service marks, typography, spacing and tokens retained. No actionable layout
  findings for this scoped change.
- Relevant frontend tests, TypeScript check, targeted backend regressions and
  production frontend build passed. Live Google delivery remains untested here.

final result: passed

---

## Previous QA reports (preserved)

# Google account permissions design QA

Source visual truth: `/var/folders/w2/ln5y7jbx4zbb58chsc0w9q2m0000gn/T/codex-clipboard-1941a458-68d4-4976-84b3-82c058be1730.png`.
Implementation evidence: `/tmp/agentworks-google-access-qa/form-dark.png`,
`edit-dark.png`, `edit-narrow-fixed.png`, `form-light.png` in the same directory.
Full-view comparison: `/tmp/agentworks-google-access-qa/comparison.png` places the
original screenshot and redesigned form together at native pixel scale.

Viewport: form and account editing at 700×900 CSS pixels; narrow sidebar at
360×820. Source: 732×275 pixels, including surrounding app edges. Implementation
captures use device scale 1 and native 700-pixel width; narrow capture uses 360.
No density scaling. The requested redesign intentionally increases form height
from the original compact dropdown grid to service cards. Comparison is of the
Google form region; it is not a claim of matching the original pixel for pixel.

State: initial connect with Gmail/Drive/Calendar read-only, other services off;
existing sample account, two pending permission edits; Cancel; dark and light.
The preview renders the production components with sample API responses. Live
account creation, consent and removal were not performed.

## Findings

No actionable P0/P1/P2 findings remain.

- Typography: existing app sans-serif, 14px service headings, 12px descriptions
  and permissions. Labels remain readable; long account names wrap.
- Spacing/layout: two service columns at reference width, one at narrow width.
  Controls and footer remain inside the form. The narrow page measures 360px
  document width for a 360px viewport, with 294px service cards.
- Colors/tokens: existing background, border, primary and muted tokens used.
  Pending access changes have both a Changed label and border tint; status is
  also conveyed in text. Light and dark variants were inspected.
- Asset fidelity: existing vendored Google, Gmail, Drive, Calendar, Docs, Sheets
  and Slides marks; no approximate custom marks. Icons remain crisp.
- Copy/content: current agent access is distinguished from pending selection.
  Notifications-only Gmail access is explained; no claim of OAuth revocation.

## Comparison history

1. P2 at 360px: action buttons squeezed the account email into an awkward final
   one-character line. Evidence: `edit-narrow.png`. Fixed by reserving a 10rem
   flex basis for account identity so actions wrap onto the next row.
2. Retest at the same viewport/state: `edit-narrow-fixed.png` shows the email
   on one line with actions below. No overflow or actionable layout issue.

Focused region comparison: all form controls and typography are readable at
native 700px capture size in the combined comparison, so no extra crop needed.
The editing and narrow screenshots cover the additional requested states.

## Implementation checklist

- Service icons, saved permission labels and Add/Remove controls implemented.
- Saved values remain visible while edits change; Cancel discards the edits.
- Existing connection ID, workspace isolation and read-only gating tested.
- Browser Add/Remove, write selection and Cancel interactions verified.
- No console errors on the corrected isolated preview origin (5220). Initial
  generic fixture import failed; the fixture now replaces the API module with
  sample responses and uses the real UI components.

Residual test gap: live Google consent was not performed in visual QA.

final result: passed

---

## Previous QA report (preserved)

# Design QA — Schedule Description Width

- Source visual truth: `/var/folders/w2/ln5y7jbx4zbb58chsc0w9q2m0000gn/T/codex-clipboard-ee9b83c3-236e-4f12-9682-7b2bf8e0bf66.png`
- Browser-rendered implementation: `/tmp/schedule-description-implementation.png`
- Combined comparison: `/tmp/schedule-description-comparison.png`
- Viewport: 1442 × 1150 CSS pixels
- Source pixels: 1442 × 1150
- Implementation pixels: 1442 × 1150
- Density normalization: equal pixel dimensions at the reference desktop viewport; no resampling used for the captured evidence
- State: dark theme, Automation Schedules → All Schedules, `sales-outreach` filter, `LinkedIn daily engagement (10/day target)` expanded

## Findings

- No actionable P0, P1, or P2 differences remain for the requested change. The expanded workshop description now uses the full card width beneath the top-right action controls instead of retaining an empty action column for its complete height.
- Fonts and typography: existing application font, weight, line height, and muted text hierarchy are unchanged. The wider measure reduces unnecessary line wrapping without changing text styling.
- Spacing and layout rhythm: the action controls remain aligned at the top-right. Header, schedule, and cron metadata reserve their top-row clearance, while the description, run statistics, issue state, and chat hint span the available width below.
- Colors and visual tokens: unchanged; the implementation continues to use the existing dark-surface, muted-foreground, amber missed-state, and purple workshop tokens.
- Image quality and asset fidelity: no raster imagery or custom assets are involved in this component; existing icon components remain unchanged.
- Copy and content: unchanged and complete.

## Full-view Comparison Evidence

At the matched 1442 × 1150 viewport, the expanded schedule preserves the existing modal and card hierarchy. The description extends underneath the action area and ends near the card's right padding, while the actions remain isolated at the top-right.

## Focused Region Comparison Evidence

The expanded `sales-outreach` schedule was inspected directly because the requested change concerns the description measure. The message line now spans the card below the header controls; no text clips, overlaps, or runs beneath the action buttons. A separate crop was unnecessary because the full-size evidence keeps the complete expanded card legible.

## Interaction And Runtime Checks

- Searched for `sales-outreach` in the schedules UI.
- Expanded the `LinkedIn daily engagement (10/day target)` schedule.
- Confirmed the long workshop description renders at full width.
- Browser console errors checked: none.
- TypeScript project build passed.

## Comparison History

- Initial source issue: the description inherited the width reserved by the top-right action column, leaving a large unused area below the controls.
- Fix: pinned the actions to the card's top-right and reserved clearance only on the header, schedule, and cron rows.
- Post-fix evidence: `/tmp/schedule-description-comparison.png`; the description and lower metadata now use the full available width.

## Implementation Checklist

- [x] Preserve action alignment.
- [x] Keep header metadata clear of actions.
- [x] Allow the description to span the full card width.
- [x] Verify the expanded state at the source viewport.
- [x] Check for browser console errors.

## Follow-up Polish

- None required for this scoped change.

final result: passed
