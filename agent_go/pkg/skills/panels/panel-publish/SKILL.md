---
name: panel-publish
description: Publish panel in Goals and Crews. Configure, perform or inspect Goal publishing.
metadata:
  panel-tools:
    agentworks:
    - settings
    - execute_shell_command
    - dashboard
  external-tools:
  - dashboard.export_static
  - update_settings
---

# Publish

Read builder-reference/references/publish-strategy.md before setup or publishing. Use scoped file and shell tools for the existing publish procedure and its declared destination; use dashboard action=link only for an authenticated internal report link. A preview or internal link is not public publishing. Inspect the saved publish configuration and actual destination/status evidence; keep tokens in selected secret environment variables.

For Surge, Vercel and all static hosts, use dashboard action=export_static on the saved publication each Publish pass and deploy its whole returned folder. It packages the same TSX/shared components and current authorized data. Preserve the saved visibility/encryption and provider settings. Keep the timestamp/live link; private chat, DB and credentials never ship. Auto-after-run Publish regenerates the export; do not create a separate dashboard or polling publisher.

For Goals, “After every run” is `publish.after_runs`, off by default. Use settings action=update to change it, preserving destination configuration. It applies to every full run and skips unchanged files; the explicit action still works while automatic runs are off. Flows do not use Goal housekeeping.
