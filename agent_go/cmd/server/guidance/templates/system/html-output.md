## HTML output: formats and runtime usage

Load this reference when writing an HTML artifact. Follow the user's design
preferences and supplied references for layout, typography, colors, charts and
navigation. This reference describes how the platform displays HTML.

### Choosing the output format

| Destination | Format |
|---|---|
| Structured data for a downstream step | JSON |
| Dashboard documents, org pages (`pulse/*.html`), published pages | HTML |
| Human-readable step reports, notes, KB entries and learnings | Markdown by default; HTML when requested or needed for the artifact |
| Downloadable raw data | JSON or CSV |

A Dashboard is a page under `db/reports/`, rather than a report regenerated
by each workflow run. Step outputs can remain Markdown, which renders in the
viewer with clickable workspace links.

### In-app Dashboard documents

The Dashboard tab loads complete HTML documents under `db/reports/`.
`index.html` is the default; other documents appear in the toolbar using their
`<title>`. Optional `views.json` controls titles, order and default selection.

The host supplies:

- `window.report` for data, workspace files, actions and optional widgets.
  Use these APIs inside `window.report.ready(fn)` so they are available at
  initial load and rerun on data refresh.
- App theme state as `class="dark"`, `data-theme="dark"`, CSS palette variables
  such as `hsl(var(--background))`, and the `report:theme` event. If styling
  depends on theme, use these signals; `prefers-color-scheme` alone follows
  the OS rather than the app setting.
- Frame height and page scrolling. Avoid fixed body/html heights and nested
  page scroll containers that conflict with the host.
- Workspace file previews and anchor navigation.

Load `reporting-policy.md` for the complete data, media, action, authorization
and Workshop/Run contracts.

### HTML and dependencies

Use a complete HTML document with a meaningful `<title>`, character encoding
and viewport metadata. CSS and JavaScript can be inline. Browser-compatible
libraries may be loaded through version-pinned HTTPS CDN URLs; the platform
does not choose a CSS framework or chart library.

For daisyUI, `<html data-report-ui="daisyui">` asks the host to inject its pinned
stylesheet. daisyUI alone does not include Tailwind utility classes.

Standalone or published HTML has no injected `window.report` runtime. Include
its required data and assets in the output. OS `prefers-color-scheme` is
available for standalone theme behavior when desired.

### Verification

Check that displayed values come from the actual evidence and that missing data
is represented accurately. Verify dependencies, file links and interactions.
For Dashboard documents, run `validate_report_html` and then `preview_report`;
inspect the browser result for runtime errors and confirm the user's requested
presentation works at the supported widths and app themes.
