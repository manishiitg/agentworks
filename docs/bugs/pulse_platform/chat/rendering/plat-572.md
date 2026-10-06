[← chat / rendering](index.md)

# PLAT-572: Absolute workspace-docs links are not clickable in chat

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P3 |
| Product | chat |
| Area | rendering |
| Summary | A chat link to an absolute disk path under workspace-docs rendered as dead grey text |

## What happened

After a step test (PLAT-562) the Upwork Builder ended its reply with "View test receipt", linked to the absolute path
`/Users/.../workspace-docs/Workflow/upwork/runs/test-.../test_mode.json`. In the chat it showed as plain grey text; in
the terminal-style view it showed the raw path in parentheses. `MarkdownRenderer` opens a link as a workspace file
only when it is workspace-relative; any other non-http link is shown as unsupported text.

A first line that looked cut off at the right edge of the same screenshot is the screenshot's own crop: the paragraph
wraps normally in the chat width.

## Fix

`resolveWorkspaceHref` now treats an absolute path that contains `/workspace-docs/` as the workspace file after that
folder, so the link opens in the workspace viewer. Access is still decided by the workspace API when the file is
opened. One test pins it. Untrusted content still never resolves workspace links.

## Left

- Agents should prefer workspace-relative links; this only stops absolute ones from looking broken.
- Absolute paths outside `workspace-docs` (for example `/tmp/...`) stay unsupported text, on purpose.
