[← platform / frontend-chat](index.md)

# PLAT-253 — "Needs your decision" card body rendered raw markdown syntax instead of formatted text

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | - |
| Product | app |
| Area | ui |
| Summary | harness_issue, severity low/cosmetic — content was fully |

| Coordination | Value |
|---|---|
| Assigned agent | Claude Code |
| Ticket state | `implemented; runtime reverify` |
| Last synchronized | `2026-08-30` |

- **Priority:** harness_issue, severity low/cosmetic — content was fully
  readable, just visually noisy (literal `##`, list markers, etc.).
- **Findings:** No workflow finding is linked. Reported live by the user
  with a screenshot of a Pulse "Needs your decision" card whose context
  body showed `## Post` and other markdown syntax literally instead of
  rendered as a heading.

## Root cause

`frontend/src/components/workflow/ReportHumanInputPanel.tsx`'s
`HumanInputContext` component parses a decision's `context` string into
sections via `parseReportHumanInputContext`, then rendered each section's
`body` as plain text:

```tsx
{section.body && <p className="whitespace-pre-line">{section.body}</p>}
```

`section.body` legitimately contains markdown (this ticket's own draft
content included a `## Post` heading, paragraphs, etc.) — it was never run
through a markdown renderer, unlike the same "decision/feedback context"
pattern elsewhere in the codebase (`BlockingHumanFeedbackDisplay.tsx`,
`PlanApprovalDisplay.tsx`, `HumanVerificationDisplay.tsx`), which already
use a shared markdown component for this exact kind of text.

## Fix

Render `section.body` through `PlainMarkdown`
(`frontend/src/components/ui/PlainMarkdown.tsx`) — the lighter of the
codebase's two markdown components, purpose-built for "content that is
read, not interacted with" (no workspace/store dependencies, typography
tuned for dense operational text). `MarkdownRenderer` was the closer match
to what's already used elsewhere for this same pattern, but `PlainMarkdown`
avoids pulling workspace/app/mode/preset store imports into a read-only
decision-context leaf component for no functional benefit here. Left
`section.items` (the separately-parsed numbered list) untouched — it
renders correctly today via a plain `<ol>`/`<li>`, and wrapping list items
individually in a markdown renderer risked double-numbering or nested-list
oddities for no reported problem.

## Verification

`npx tsc --noEmit` and `npx eslint
src/components/workflow/ReportHumanInputPanel.tsx` both clean, zero
warnings.

## Reverify

Confirm live: open a pending Pulse decision whose context includes
markdown (a `##` heading, bold text, etc.) and check it renders formatted
instead of showing the raw syntax.
