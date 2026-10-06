[← platform / integrations](index.md)

# PLAT-319 — Slack notifications display plain text and formatted attachment twice

| Field | Value |
|---|---|
| State | deployed |
| Priority | - |
| Product | platform |
| Area | integrations |
| Summary | The shared Slack webhook formatter sent the full message as top-level `text` |

| Coordination | Value |
|---|---|
| Assigned agent | Codex |
| Ticket state | Slack fix deployed to RTS; reported email duplication not reproduced by code tests |
| Last synchronized | 2026-09-12 |

The shared Slack webhook formatter sent the full message as top-level `text`
and repeated it in attachment Block Kit sections. Top-level text is a visible
message alongside secondary attachments, not their hidden fallback.

The formatter now retains the styled attachment and puts the plain alternative
in that attachment's `fallback` field. Tests assert that no top-level text is
sent, the fallback survives and the formatted body appears once. This shared
path applies across workflows and local/server deployments. No test messages
were sent to real Slack channels.

Reference: https://docs.slack.dev/legacy/legacy-messaging/legacy-secondary-message-attachments

The user also reported email duplication and requested code-only investigation.
The shared notifier passes plain text and Gmail HTML separately. Gmail builds
one multipart/alternative body inside multipart/mixed; gws and gog raw delivery
preserve that MIME structure. A parser-based test verifies one plain alternative,
one HTML alternative and no additional visible body. It does not reproduce the
reported email symptom, nor prove that caller-authored HTML never repeats itself.
Email duplication remains unverified; do not close it based on the Slack fix.

RTS deployment verified on 2026-09-12: release `ee63424-20260912144418`,
application revision `ee634249d61c606fc0e6077486a661a0285bdf06`.
All three services and public health passed. The host isolation regression
passed with the deployed GOG_HOME configuration.
