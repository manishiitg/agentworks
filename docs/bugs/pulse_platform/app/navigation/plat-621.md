[← app / navigation](index.md)

# PLAT-621: Shortcut hint on new chats

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P3 |
| Product | app |
| Area | navigation |
| Summary | The start card of a new Code project, Crew or workflow shows the keys worth knowing |

## What happened

Owner, 2026-10-06, after the shortcut clean-up (PLAT-617): when creating a new Code project, Crew or workflow, show
the user the shortcuts.

## Fix

`ShortcutHint` (`components/chat/ShortcutHint.tsx`) is a line at the bottom of the empty-chat start card:
⌘K / Ctrl+K to jump to any product, project or chat, Esc to stop a running chat, Shift+Enter for a new line; Code
adds "New tab" for another chat in the project. Shown on the Code, Crew (own and shared) and workflow/Relay start
cards; it goes away with the card once the chat has a message. `ProductChatLandingCard` takes `shortcutHint`.
`tsc -b`, ESLint and the Work, workflow layout and chat tests pass.
