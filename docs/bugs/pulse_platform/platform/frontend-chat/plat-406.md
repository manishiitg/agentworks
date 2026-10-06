[← platform / frontend-chat](index.md)

# PLAT-406 — Chat composer: New chat in Code, mic, layout and Code toolbar order, no model picker

| Field | Value |
|---|---|
| State | deployed |
| Priority | P3 |
| Product | platform |
| Area | frontend-chat |
| Summary | fixed on `main`; New chat deployed widely, mic and layout on Excellence only. |

| Coordination | Value |
|---|---|
| State | fixed on `main`; New chat (`1b3292985`) is in Excellence `agents-0cf68aa9` / `agents-e7db4f50`, Confida `confida-23270875`, Dominion `5e15f373`, SparkQuill `sparkquill-49a1e676`; mic and right-hand layout (`da63d5118`) and left-hand terminal/attach (`e7db4f50f`) are in the two Excellence releases only, deploy pending elsewhere |
| Severity | P3 |
| Date | 2026-10-03 |
| Owner | frontend-chat |
| Related | PLAT-403 (terminal), PLAT-402 (no Native agent tools toggle), PLAT-404 (New chat button styling shipped with the socket-folder fix) |

Commits: `1b3292985`, `da63d5118`, `e7db4f50f`, `ba95c2f3c` (quieter New chat button).

## What was done

- **New chat in Code (user).** Code's composer shows the existing New chat action (owner only, not on a shared Code). It stops the running session, rotates the
  project's conversation on the server (the project manifest gets a new session id, so the coding agent starts a fresh conversation) and resets the same tab; the previous
  conversation stays listed. Code always has one active tab: it never opens a parallel chat. The button is icon only in the composer's neutral colours, "New chat" slides out
  on hover/focus (`ba95c2f3c`).
- **Model picker removed (user).** The chat input no longer renders a model/reasoning picker on any surface (it only showed for `inputVariant="product"`: Video Studio,
  Dominion, SparkQuill). Models change in each product's settings; a workflow's model lives in its LLM configuration panel.
- **Mic and order in Code (owner).** The mic is on for Code (`product.yaml` declares `voice: preferred`; needs the server's speech engine, Excellence has it). `ChatInput`
  renders New chat and the live-view control inside the send controls; `WorkWorkspaceToolbar` orders Code's groups Dashboard | Files, Terminal, Browser | Automation, Costs |
  Setup (a Crew's order is unchanged).
- **Composer layout (owner).** In every chat input the live-view (terminal) toggle and the attach button sit on the left; New chat, the commands (wand), the mic and send on
  the right. The live-view toggle had been moved right by mistake earlier the same day ("browser commands" meant the wand). The workflow Models page no longer offers
  "Native agent tools" (always on, see PLAT-402); a value saved as off is untouched but not editable.

## Left

- Deploy `da63d5118` and `e7db4f50f` beyond Excellence.

## Register notes

[PLAT-406](plat-406.md), fixed on `main`; New chat deployed widely, mic and layout on
Excellence only. No model picker in the chat input; terminal and attach left, New chat, wand, mic and send right.
