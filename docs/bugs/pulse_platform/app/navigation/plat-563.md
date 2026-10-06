[← app / navigation](index.md)

# PLAT-563: Sidebar product switching is overridden by workflow restoration

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | app |
| Area | navigation |
| Summary | Fixed on main; not deployed. Sidebar Code navigation keeps its destination. |

## What happened

The owner shared a report that repeatedly selecting Code from the left product
menu returned to Goals, while Ctrl+K worked.

The sidebar selected a product without setting the project-chat mode. The new
Code chat could therefore mount its workflow handler with Goals' old mode;
that handler's preset restoration selected Goals again. Opening an existing
Code chat through Ctrl+K activates its tab and mode synchronously, avoiding
that startup window.

Two delayed paths could also reclaim the screen: an unmounted workflow handler
continued selecting its preset after manifest loading, and a pending workflow
navigation remained current because product switches retained the active preset.

## Fix

- Set both chat mode projections before mounting Code, Crew or Brain; set both
  workflow mode projections for Goals/Relays. Shared product navigation serves
  the left menu, Ctrl+K product entries and workspace Back actions.
- Cancel pending workflow-navigation generations on product selection, preserving
  the saved workflow and project selection for later return.
- Stop the workflow handler's asynchronous preset-selection callback when its
  effect is disposed or the handler unmounts.

## Verification

- Reproduced the Code → Goals bounce through the production sidebar, real stores
  and real WorkflowModeHandler with network manifest/file reads stubbed. The
  fixture mirrors WorkSurface's chat-mode selection and parent mount effect.
  This regression failed before the mode fix, then passed.
- A separate delayed-manifest scenario still bounced after the mode fix; it
  passed after cancelling the unmounted handler. The generation regression
  confirms a delayed tab activation cannot reclaim Goals after choosing Code.
- 44 tests across eight navigation/restore files passed, including sidebar and
  Ctrl+K navigation. TypeScript and targeted lint passed.
- This is local React integration verification, not a check of Vaibhav's deployed
  instance or the screen recording (only its thumbnail was provided).

## Left

Deploy the frontend, then verify Code switching through Vaibhav's left menu.
