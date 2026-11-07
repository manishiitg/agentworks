---
name: chat-visuals
description: Present an explanation in app chat with native diagrams, swimlanes, sequences, charts, cards or timelines in app chat or Slack. Use when a visual makes the answer clearer.
metadata:
  version: 1.1.0
  panel-tools: {}
  external-tools: []
---

# Chat visuals

Choose a visual for the person's explanation, using their observed facts. Use a
swimlane for who does each step, a sequence for ordered calls/replies, a diagram
for branches or architecture, and a chart for numbers. Ordinary comparisons
usually need only a Markdown table.

Read [formats and examples](references/formats.md) before authoring a `chat-ui`
block. Use the documented json-render SpecStream operations, not the retired
single-object format. Keep a useful written answer alongside the visual. This skill changes
presentation only; it grants no tools, access, account connections or actions.

For a reply arriving through Slack, also read [Slack delivery](references/slack.md). The same widget data is adapted to native Slack blocks; sending, routing and access remain the platform’s job.
