[← coding-agents / files](index.md)

# PLAT-720: Local Code connections, Downloads grants and guarded MCP writes

| Field | Value |
|---|---|
| State | open |
| Priority | P1 |
| Product | coding-agents |
| Area | files |
| Summary | Server Code agents use explicitly granted laptop files and commands through the outbound CLI, with guarded MCP writes for local agents using server workflows. |

## What happened

The workflow router introduced a second local-to-server path alongside public
MCP. Website Code chats also needed an explicit, limited way to operate on a
laptop project without moving the agent/model off the server. Review found
retained-tool authority, file guard/lock and rootless replacement gaps.

## Fix

- Remove router/placement APIs; local agents use public MCP with guarded writes,
  revisions, workflow-scoped locking and authenticated edit receipts.
- Add an outbound authenticated laptop CLI executor and a minimal Local Code
  mode using existing shell/patch tools. Disable unrelated server capabilities.
- Change modes only from right-side connection settings after reviewing
  consequences; reject automated and non-owner turns, fail offline without fallback.
- Add optional `--downloads`: separately approve read/write Downloads alongside
  the project, off by default, with exclusions and guarded patch routing.
- Provide install/sign-in/share steps, safe copyable commands, explicit Downloads
  consent, live connection/permission summaries and offline recovery guidance.
- Restrict Local macOS Mach access to named directory services. Real curl,
  shallow git clone, npm install/test work; desktop/Apple Events/clipboard and
  outside-key fixture probes are denied. Shell remains always enabled per owner.
- Fix rootless Linux atomic writes: preserve group and effective ACL permissions,
  retaining former-owner access without widening other masked entries. Exercise
  actual unprivileged service/slot/limited-user processes in a Linux container.
- Enable Local chat images/text uploads via picker, paste and drop. Disclose
  server/model transfer, bound text previews, and scope the existing image tool
  to current-turn uploads. Nested image CLIs use temporary copies under Landlock,
  without widening server/laptop file access or copying uploads to the laptop.
- Discover bundled server mcpbridge binaries and document deployment requirements.

## Left

- Run the full suites and deployed remote end-to-end checks; production Linux
  sandbox execution remains unverified. Existing frontend CI failures reproduce
  on the main baseline and are outside this change.
- Deploy/restart the updated server with mcpbridge installed to fix affected
  website deployments. Backend replicas require device routing affinity.
- Receipts need operator retention policy. CLI release installation and live
  login/connection were not exercised against a production server.

PR: https://github.com/manishiitg/agentworks/pull/272
