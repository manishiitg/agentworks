---
name: motion-guide
description: "Interactive dashboard playback and optional snapshot MP4 export; user-owned design and layout."
external: true
external_scopes: ["dashboards:read"]
external_tools: ["export_dashboard_motion"]
---
## Interactive motion and optional video export

Use motion when the person requests it. They choose the layout, appearance,
charts, controls, pacing and animation style; the platform supplies playback
and export mechanics. Do not add a standard motion layout or required palette.

### Use real dated data

A dashboard does not automatically keep the history of every number. Query
existing dated records, saved run results or a source's historical API through
`window.report.query`, `get`, `run` or `getRelayRuns` as appropriate. Keep the
units, date range and source with the result. If only the latest value exists,
say that history is unavailable; never invent earlier values or treat a missing
value as zero. Recording future snapshots is separate data work, not a side
effect of animation or dashboard refresh.

### Playback inside the dashboard

The shared app and preview runtime provides `window.report.motion.create` inside
`window.report.ready`. It adds no HTML, CSS, chart type or fixed controls:

```js
let player;
let refresh = 0;
window.report.ready(async () => {
  const version = ++refresh;
  const rows = await window.report.query(
    'SELECT day, total FROM daily_sales ORDER BY day'
  );
  if (version !== refresh) return; // A newer refresh owns the display.
  if (!rows.length) {
    player?.destroy();
    showEmptyHistory(); // Your authored empty state.
    return;
  }
  player = window.report.motion.create({
    id: 'sales-history',
    duration: 12, // Playback seconds, not days of source data.
    render: ({ progress }) => {
      const index = Math.min(rows.length - 1, Math.floor(progress * rows.length));
      drawRecordedValues(rows.slice(0, index + 1)); // Your chart/DOM renderer.
    },
    onChange: state => updateYourControls(state)
  });
});
// Wire the designer's controls once, outside the refresh callback:
playButton.onclick = () => player?.play();
pauseButton.onclick = () => player?.pause();
replayButton.onclick = () => player?.replay();
slider.oninput = () => player?.seek(Number(slider.value));
```

The example's drawing, controls and table are authored choices, not built-in
widgets or a required design. Use existing tables/fields rather than creating
this example's schema automatically. The slider range should be 0 to duration.
`setSpeed(number)` accepts a positive playback multiplier. `getState()` returns
`{ time, duration, progress, playing, speed, reducedMotion }`; `time` and
`duration` are seconds and `progress` is 0–1. `destroy()` releases the player.

`render` and `onChange` are synchronous. Fetch data before creating the player;
render from absolute time/progress so backward seeking produces the same state.
CSS, SVG, Canvas, Web Animations and browser-compatible libraries can use the
clock. Keep values tied to real observations; disclose any interpolation.
A stable id replaces its prior player on refresh while keeping position and
speed, so one report does not accumulate clocks. Reuse the id only for the same
sequence. The runtime starts paused. Reduced motion initially shows the end
state; people can still play and seek. Hidden pages pause. Playback does not
query data, start runs or change product permissions. Do not start playback
from a refresh callback. Cancel stale asynchronous data reads as above.

### Optional MP4

A Builder can ask: “Export this animation as a video.” External MCP uses
`dashboard` with `action: "export_motion"`, the project selector and
`composition_path: "code/motion/sales-history"` and a unique `submission_id`.
Retry with the same submission id only for the same export. The reply returns
`export_id` and `status` immediately. Check progress by calling the same action
with that `export_id`; completed replies contain `result` with the saved paths,
and failed replies explain what to fix. App Builder uses the same
server operation. This requires current project authoring access and, over
MCP, the same authoring consent plus `dashboards:read` and `runs:execute`.
Run people can watch/download a shared export; this does not give them Builder
execution. Export never starts on page load, polling or data refresh.

Author a standalone HyperFrames composition in that folder, with `index.html`,
local assets, and `snapshot.json` containing `as_of` (ISO timestamp), `source`
(source/query and units), and `data` (the chosen recorded results). Reuse the
same chart drawing logic where practical. Read HyperFrames' current composition
contract when available. The export composition reads this local snapshot;
it cannot depend on `window.report`, app login state or live data queries.
A whole interactive dashboard is not automatically a video composition.

The core export service uses the HyperFrames version pinned in the platform code, copies
the source and snapshot to a new project asset folder, checks the renderer,
renders and decodes the MP4, and puts `path`, `source_path`, `snapshot_path`,
`as_of`, duration, dimensions and bytes in the completed job result. It needs Node/npm, Chrome, FFmpeg and
FFprobe on that server. If unavailable, say video export is unavailable there;
interactive dashboard motion still works. This capability has no dependency on
a personal product. Limits: ten minutes per export, 128 MB source, 256 MB video.

Present the returned video through `window.report.mediaUrl(path)` and a native
`<video controls playsinline preload="metadata">` element if requested. Obtain
a fresh URL when opened, and keep the workspace path rather than an expiring
URL. Persist the player through refresh if its video path has not changed.
Show the snapshot time where freshness matters: an MP4 does not update when
the dashboard refreshes. Keep the exported source for requested revisions.
The designer chooses whether and where to offer playback or download.

### Check the actual result

Run `validate_report_html`/`preview_report` (or managed dashboard validate and
preview), then exercise play, pause, backward seek, replay, speed and refresh
in the browser. Check the requested layout in both themes and reduced motion.
For MP4, inspect playback and representative frames of the returned file;
successful encoding alone does not prove the chart or explanation is correct.
