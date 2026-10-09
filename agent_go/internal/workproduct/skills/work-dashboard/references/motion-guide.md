## Motion usage: animated Dashboards and MP4 explainers

Load this reference when the user requests motion. It describes the existing
Dashboard runtime and the Video Studio export path. Style, layout, pacing,
transitions, fonts, colors and chart presentation follow the user's preferences
and references. Do not add animation to an unrelated Dashboard request.

### Choose the output the user requested

| Output | Existing path |
|---|---|
| Live animated Dashboard | HTML/CSS/JavaScript under `db/reports/`, using `window.report` for data |
| Shareable video explainer | Editable composition in Video Studio, rendered to MP4 |
| Both | Dashboard for current data; a video from a recorded data snapshot |

A guide does not grant tools or install a renderer. Inspect the active product's
attached skills and available tools before using a path. Dashboard viewers
cannot invoke arbitrary rendering commands through the report bridge.

### Animation inside a Dashboard

- Use the user's chosen browser-compatible animation implementation: CSS,
  Web Animations API, SVG/canvas, or a version-pinned HTTPS browser library.
  The platform does not require an animation framework.
- Read actual data inside `window.report.ready(async () => { ... })`. The
  callback also runs on refresh. Keep data loading separate from animation
  initialization so refresh does not create duplicate timelines, event handlers
  or animation loops. Update existing elements when the data changes.
- Store controllers/players on the document or persistent elements rather than
  only in a callback-local variable. Cancel replaced animations and prevent
  older asynchronous reads from overwriting a newer result.
- Use the host's app theme signals. Do not infer the Dashboard theme from the
  OS. Animation must continue to use the same data and permission APIs as a
  static Dashboard; an animation library receives no additional access.
- If motion runs automatically, respect `prefers-reduced-motion`. Keep data and
  actions usable when motion is disabled. For an explainer in the page,
  provide user controls to start, pause, replay and seek when supported.
- Show loading, unavailable and error states accurately. Never turn missing
  values into zero or animate fabricated numbers as actual results.
- Run `validate_report_html` and `preview_report`. Preview checks rendering and
  data errors; also exercise the animation controls and refresh path in the
  managed browser. Check that refresh does not restart unchanged media or leave
  old loops running.

### A standalone MP4 explainer

1. Record the requested content, duration, aspect ratio and any supplied style
   references. Use the requested preferences rather than a platform look.
2. Capture the facts for this export once: query results, units, time window,
   source/query identifiers, and an `as_of` timestamp. Save this snapshot beside
   the editable source. Resolve assets to files the renderer can read.
   Do not query a changing system separately for every rendered frame.
3. Use Video Studio's existing HyperFrames capability when it is available.
   Load its attached `hyperframes` entry skill and the relevant runtime/CLI
   references. Inspect CLI help and run its environment checks before rendering.
   The Video Studio manifest supplies HyperFrames, Chrome and FFmpeg
   prerequisites; dashboard-only projects do not automatically have them.
4. Keep the editable composition and its data/assets together. For direct
   Video Studio work, intermediates live under `work/` and exports under
   `outputs/`. A workflow stage uses its own stage folder. Time must be
   deterministic: the same playback position yields the same frame, including
   when seeking backward or rendering frames out of order.
5. Preview and render using the available renderer's documented commands.
   Verify the exact MP4's duration, dimensions, playback and representative
   frames. In Video Studio, `show_video` presents a preview; its existing
   quality-report contract is required before marking an export approved final.
   Do not invent a new Dashboard `render_motion` or `export_mp4` API.

Paid assets and publishing remain separate actions governed by the user's
request and the active product's permissions. A deterministic animation does
not require purchasing AI-generated footage.

### Show an export in the Dashboard

If the user wants the exported explainer beside live data, transfer the final
MP4 through permitted workspace file operations into `db/assets/` and store
that workspace-relative path. Obtain a fresh playback URL with
`window.report.mediaUrl(path)` when opening a native `<video controls
playsinline preload="metadata">` player. Do not store its expiring URL or
embed the MP4 as a data URL. Preserve the player through data refresh when the
export has not changed.

The MP4 represents its saved snapshot; it does not become current when the
Dashboard refreshes. Show the snapshot's timestamp wherever the export's
freshness matters. Regenerating it is an explicit production action, not a
side effect of opening, rendering or polling a Dashboard.
