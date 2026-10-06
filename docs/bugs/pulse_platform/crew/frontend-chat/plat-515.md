[← crew / frontend-chat](index.md)

# PLAT-515 — Many template "Setup pending" rows took over the Crew chat

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | - |
| Product | crew |
| Area | frontend-chat |
| Summary | fixed on main: the rows share one capped, scrolling area. |

| Coordination | Value |
|---|---|
| State | fixed on main; reload the frontend. Seen only in the screenshot; not looked at in a browser after the fix |
| Date | 2026-10-05 |
| Owner | frontend-chat |

## Source

Owner screenshot (Crews, a project with many templates): every template added its own full-width "<Template> · Setup pending / Set up in chat" row above the chat, so six or more rows filled the
screen and left no room for the conversation.

## Done

- `WorkSurface.tsx` puts the template setup rows in one container capped at `min(8rem, 25vh)` that scrolls (`data-testid="template-setup-list"`), so the chat keeps most of the height. The rows themselves are unchanged.

## Left

- Not seen in a real browser. With the cap, about three rows are visible at a time and the rest scroll; if a different look is wanted (a collapsed "N templates need setup" summary, or only the first row plus a count),
  that is a follow-up.

## Register notes

[PLAT-515](plat-515.md), fixed on main: the rows share one capped, scrolling area. Left: not seen in a browser.
