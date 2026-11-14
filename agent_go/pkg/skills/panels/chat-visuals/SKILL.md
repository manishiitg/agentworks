---
name: chat-visuals
description: Present useful images, coding summaries, diagrams, swimlanes, sequences, charts, cards, timelines or tables in app chat or Slack. Use when a visual makes the answer clearer.
metadata:
  version: 1.4.0
  panel-tools: {}
  external-tools: []
---

# Chat visuals

Choose a visual for the person's explanation, using their observed facts. Use a
swimlane for who does each step, a sequence for ordered calls/replies, a diagram
for branches or architecture, and a chart for numbers. Ordinary comparisons
usually need only a Markdown table. Show screenshots, designs and other relevant
visual results inline without waiting for the person to ask for a component.
For a single image in app chat, prefer `![caption](Crew/example/assets/image.png)`
using an actual existing workspace path or HTTP(S) URL. No card/title/JSON is
needed. Consecutive Markdown images automatically form a compact gallery; put
prose between them when they should stay separate. The app handles authenticated workspace loading, enlarge and download.
For coding work in app chat, use `file_changes` for observed file changes and
`checks` for actual build/test/lint results when clearer than prose. Read the
format reference. Omit unknown line counts and links; never imply a check passed
when it was not run. These are reported snapshots, not live execution controls.
Keep ordinary answers as text; do not turn every reply into a visual.

Read [formats and examples](references/formats.md) before authoring a `chat-ui`
block. Use the documented json-render SpecStream operations, not the retired
single-object format. Keep a useful written answer alongside the visual. This skill changes
presentation only; it grants no tools, access, account connections or actions.

For a reply arriving through Slack, also read [Slack delivery](references/slack.md). The same widget data is adapted to native Slack blocks; sending, routing and access remain the platform’s job.
