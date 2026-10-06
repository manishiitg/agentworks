[← platform / frontend-chat](index.md)

# PLAT-527 — Chat scroll flickers after switching between workflows (cause not found)

| Field | Value |
|---|---|
| State | open |
| Priority | P2 |
| Product | app |
| Area | ui |
| Summary | open: cause not found, three suspects, needs a reproduction. |

| Coordination | Value |
|---|---|
| State | open: cause not confirmed; owner chose to keep it open (2026-10-05) |
| Priority | P2 |
| Date | 2026-10-05 |
| Owner | frontend-chat |

## Source

Owner, local app: "the chat flickers a lot", then "scroll filter is there even now", then "it starts after we switch between workflows". Fine before any switch.

## Checked (code and history read only; nothing run in the app)

- Workflow to Crew unmounts the old chat (`App.tsx:933-985`); switching between workflows keeps one `ChatArea` mounted and only the `tabId` changes (`WorkflowLayout.tsx:2470`).
- No leaking scroll observers, listeners or intervals found (`useTranscriptScroll.ts`, `ChatArea.tsx`, `WorkflowLayout.tsx`).
- The outer chat scroller steps aside while the transcript shows (`ChatArea.tsx:3565`), so two scrollers do not fight over one container.
- Server log: one chat re-subscribed to its event stream several times within seconds around the switches (18:55 to 18:58); no errors.

## Suspects (none confirmed)

1. The follow-the-bottom logic fights Virtuoso after the list rebuilds with unmeasured rows (`useTranscriptScroll.ts:174-191, 214-215, 324-337`; `TerminalEventTranscript.tsx:1044`). A code comment already says it "can fight itself".
2. Older history pages cached per chat (commit `89e1040fa`, 2026-10-05) are joined to live events without de-duplicating by event id (`ChatArea.tsx:876-880`); an overlap would give duplicate row keys and re-measuring. Only if earlier pages were loaded. Overlap not shown.
3. The `manual` flag stays set after a wheel gesture, so a layout-driven scroll counts as the reader scrolling up and later height changes shift the view (`useTranscriptScroll.ts:270-312, 331-334`).

## Left

- Reproduce and say which suspect: a short screen recording, whether it needs scrolling up or "Load earlier" first, or a temporary log of `move` / `scrollToIndex` calls after a switch.
- Then fix the confirmed one only; do not ship a guess into the recently reworked scroll code.

## Register notes

[PLAT-527](plat-527.md), open: cause not found, three suspects, needs a reproduction.
