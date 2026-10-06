[← app / navigation](index.md)

# PLAT-617: Remove old keyboard shortcuts

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P3 |
| Product | app |
| Area | navigation |
| Summary | Only Cmd/Ctrl+K stays app-wide; the Goals-era Ctrl+1/3/6/7 are removed and the shortcuts panel shows in every product |

## What happened

Owner, 2026-10-06: the app-wide shortcuts predate the other products. `App.tsx` handled Ctrl/Cmd+1 (switch to
workflow mode), +3 (workflows overview), +6 (minimise the workflow workspace) and +7 (toggle chat auto-scroll). All but
K were Goals-only, the numbers took the browser's own tab keys in the web app, and the shortcuts panel used old names
("Automation", "Parallel Automations") and was reachable only in Goals.

## Fix

- Only Cmd/Ctrl+K (quick switcher) stays app-wide; the 1/3/6/7 handlers and the code only they used are removed.
- The shortcuts panel lists Cmd/Ctrl+K and the chat keys every product already handles (Enter sends, Shift+Enter new
  line, Esc stops a running chat), and is in the account menu of every product, not only Goals.
- Code's terminal keeps its own Alt+1/2/3 and Alt+Shift+T tab keys.

`tsc -b`, ESLint and the top bar and App tests pass.

## Left

- Proposed to the owner, not built: Alt+1 to Alt+4 switch Code chat tabs and Alt+Shift+T opens one, matching the
  terminal's keys.
