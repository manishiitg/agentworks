---
name: motion-guide
description: "Interactive dashboard playback and optional snapshot MP4 export; user-owned design and layout."
external: true
external_scopes: ["dashboards:read"]
external_tools: ["dashboard.export_motion"]
---
## Interactive motion and optional video export

Use motion when the person requests it. They choose the layout, appearance,
charts, controls, pacing and animation style; the platform supplies playback
and export mechanics. Use the shared floating MotionControls toolbar by default;
this does not prescribe the page layout or palette. Honor requests for different
controls, styling or placement.

### Make motion easy to find

For an interactive playback page, set `motion: true` with dashboard action=create or
dashboard action=update (MCP: dashboard action=create/update), then publish the revision.
This puts it under **Motion dashboards** in the project menu. It is the same
dashboard and live link, with the same Share and access rules. The flag is a
label; it does not create animation or a video. Omit it on ordinary updates to
keep the saved label; use false and publish to move the page back. For an
existing managed motion page, update only this flag with its expected_revision;
keep its files, design and data intact. Never classify a page from its title.

Explain it as “Play and explore changes over time, or follow a visual explanation
one step at a time.” Examples: monthly sales, spending over time, project
progress and process walkthroughs. Offer a small first version using available
dated data. Ask only for missing data or business meaning; use fictional data
only when the person requests a demo, and label it clearly. Keep appearance and
layout editable. New TSX pages use the shared MotionControls component by default,
passing the report.motion clock and driving chart state from its render callback.

Interactive playback is a browser clock, not a HyperFrames video. HyperFrames
is used on the server for optional MP4 rendering. Playback does not run Python
or query data on each frame. Export freezes the loaded data and seeks the same
authored page frame by frame; a separate explainer is an explicit choice.

### Shared floating toolbar (default)

Import `MotionControls` from `@agentworks/dashboard`. It supplies a single
Play/Pause toggle, Replay, a themed seek slider and a 0.5×/1×/2× speed selector.
It subscribes to the shared clock itself; do not add another playback timer or
duplicate Play/Pause/speed buttons. Refresh data is a separate action.

Place it inside the designer's `position: relative` playback area. It floats
near that area's bottom, never fixed over the whole app. Reserve enough space
below the chart for the toolbar (about 110px on narrow screens; inspect the real
preview). Set `containerType: 'inline-size'` on that area so controls wrap to its
width even in a narrow desktop panel. Do not cover chart labels or important data.

```tsx
// Inside the authored page; `clock` comes from report.motion.create(...).
// Its render callback updates progress and the actual observation label.
const playbackStyle = { position: 'relative', containerType: 'inline-size', paddingBottom: 120 };
<section style={playbackStyle}>
  <LineChart data={rows} progress={progress} />
  <MotionControls clock={clock}
    positionLabel={currentObservation.label}
    detail={`Day ${currentIndex + 1} of ${rows.length}`} />
</section>
```

`positionLabel` and `detail` are supplied from real observations or process
steps, never guessed from elapsed seconds. Omit them when not meaningful.
`label` sets the group's accessible name. `placement="inline"` places it in
normal flow; `showReplay`, `showSpeed` and `showTime` can hide optional controls.
`speeds` supplies positive speed choices. `className`, `style`, theme tokens and
`--motion-toolbar-offset` allow a different appearance, width or placement.
The designer may place it above, below or over their visualization, and compose
custom controls when requested. No required sidebar, layout or palette.

Check Play/Pause, Replay, seeking backward and to the end, speed, keyboard
focus and slider arrow/Home/End keys in desktop and mobile previews. An
unavailable clock disables controls. Reduced motion, paused starts and hidden
pages retain the shared clock's behavior. Export seeks the same clock and
updates the same toolbar; do not reconstruct it for MP4.

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
// HTML pages or explicitly custom controls only; new TSX uses MotionControls.
// Wire these once, outside the refresh callback:
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
`duration` are seconds and `progress` is 0–1. `subscribe(listener)` immediately supplies the current state and returns an
idempotent unsubscribe function; later clock changes notify subscribers without
an extra timer. `destroy()` releases the player and its subscriptions.

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

A Builder can ask: “Export this dashboard as a video.” External MCP uses
`dashboard` with `action: "export_motion"`, the project selector,
`dashboard_id` and a unique `submission_id`. Dashboard export is the default:
it uses the exact published HTML and rendering runtime, with frozen data.
Pass the person's selected `theme` and viewport (`viewport_width`,
`viewport_height`) when available. Otherwise state the captured default
(dark, 1280×720), rather than claiming it matches an unknown screen size.
Optional `revision` selects an exact published version. Optional `duration_s`
and `fps` choose length and frame rate; normally the shared clock determines
duration, or a still page lasts one second. The final encoded frame includes
the terminal state, even when a tween ends exactly at the duration.

Retrying the same submission id returns the original export and snapshot,
even after a new publication. The reply returns `export_id` and `status`
immediately. Poll the same action with `export_id`; completed replies contain
`result`, and failures explain what to fix. App Builder uses this same path.
Existing authoring access and, over MCP, `dashboards:read`, project authoring
consent and `runs:execute`, still apply. Run people watch/download shared
exports; they do not gain Builder execution. Export never starts on page load,
polling or refresh.

Use the existing `window.report.motion` clock or seekable CSS/Web Animations.
A page without a seekable clock still exports its settled view as a still video
and returns a plain note. To animate it, connect the existing drawing code to
the shared clock; do not rebuild its layout, colors or values for export.
Data calls are captured once before seeking. Frames reuse those saved results.

A separately authored explainer remains explicit: pass
`source_mode: "composition"` and `composition_path: "code/motion/sales-history"`
(the existing composition_path alone also works). That folder needs HyperFrames
`index.html`, local assets and `snapshot.json` with `as_of`, `source` and `data`.
Read the composition contract when available. Use the same saved data and the
person's requested theme; describe a redesigned explainer as a separate video.

The core export service uses the HyperFrames version pinned in the platform code, copies
the source and snapshot to a new project asset folder, checks the renderer,
renders and decodes the MP4, and puts `path`, `source_path`, `snapshot_path`,
`as_of`, duration, dimensions and bytes in the completed job result. It needs Node/npm, Chrome, FFmpeg and
FFprobe on that server. If unavailable, say video export is unavailable there;
interactive dashboard motion still works. This capability has no dependency on
a personal product. Limits: ten minutes per export, 128 MB source, 256 MB video.

Use completed `result.path` exactly; it is under `db/assets/motion/`. Do not
replace it with `outputs/` or `code/motion/` paths. Present the video through
`window.report.mediaUrl(result.path)` and a native
`<video controls playsinline preload="metadata">` element if requested. Obtain
a fresh URL when opened, and keep the workspace path rather than an expiring
URL. Persist the player through refresh if its video path has not changed.
Handle `loadedmetadata` and media `error`: show its duration or a useful loading
error rather than an unexplained 0:00 player. Show the snapshot time where
freshness matters: an MP4 does not update when
the dashboard refreshes. Keep the exported source for requested revisions.
The designer chooses whether and where to offer playback or download.

### Check the actual result

Run `dashboard action=validate`/`dashboard action=preview` (or managed dashboard validate and
preview), then exercise play, pause, backward seek, replay, speed and refresh
in the browser. Check the requested layout in both themes and reduced motion.
For MP4, check metadata, playback and seeking through the actual media URL.
Compare representative frames and the final decoded frame with the captured
dashboard revision, theme, viewport and saved values;
successful encoding alone does not prove the chart or explanation is correct.
