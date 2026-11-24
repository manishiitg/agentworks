---
name: chat-visuals
description: Present useful images, coding summaries, diagrams, swimlanes, sequences, charts, cards, timelines or tables in app chat or Slack. Use when a visual makes the answer clearer.
metadata:
  version: 2.0.0
  panel-tools: {}
  external-tools: []
---

# Chat visuals

Show it, do not only describe it. Use a native visual whenever the answer has a
flow, roles, an order of calls, numbers, a history, a set of images or a coding
result:

- flow, process or architecture: `diagram`
- who does each step: `swimlane`
- calls and replies in order: `sequence`
- numbers: `chart`
- events over time: `timeline`
- places, options or images with text: `cards`
- coding work: `file_changes` and `checks`
- a short comparison: an ordinary Markdown table (or `table` for larger saved data)

Never draw a diagram with text characters (arrows, boxes or ASCII art in a code
block): use a `diagram`, `swimlane` or `sequence` instead. A visual must use real
facts from the work; never invent values, times, calls or evidence. Keep a
sentence or two of prose with it, with the main answer and key numbers outside
the visual. This skill changes presentation only; it grants no tools or access.

## The format: one line per item, so it streams

Write a fenced `chat-ui` block of Vercel json-render SpecStream: one complete
JSON patch per line. Line 1 sets the root; line 2 adds the widget with its title
and EMPTY lists; then add one row, node, step or event per line at the list's
`/-` path. The person sees the visual grow as each line arrives.

```chat-ui
{"op":"add","path":"/root","value":"visual"}
{"op":"add","path":"/elements/visual","value":{"type":"diagram","props":{"title":"How a request moves","direction":"horizontal","nodes":[],"edges":[]},"children":[]}}
{"op":"add","path":"/elements/visual/props/nodes/-","value":{"id":"ask","title":"Person asks"}}
{"op":"add","path":"/elements/visual/props/nodes/-","value":{"id":"work","title":"Agent prepares the answer","emphasis":true}}
{"op":"add","path":"/elements/visual/props/edges/-","value":{"from":"ask","to":"work"}}
{"op":"add","path":"/elements/visual/props/nodes/-","value":{"id":"save","title":"Result saved"}}
{"op":"add","path":"/elements/visual/props/edges/-","value":{"from":"work","to":"save"}}
```

Rules: one widget per fence (use several fences around your prose when useful);
add a node before an edge that uses it, and lanes/participants before their
steps/messages; never send a half-written line, repeat an earlier append, or
add a raw JSON copy. Only `add`, `replace` and `remove` work. At most 256 lines
and 50,000 characters per block. While writing, an empty widget shows "Preparing
visual"; a finished block that is invalid shows a collapsed "View source".
These blocks show saved facts, not a live query. Do not write HTML, CSS,
scripts, page layouts or metric cards in chat; use a Dashboard for a reusable
page.

## Widgets and their fields

Every widget needs `title` (up to 160 characters) unless noted. Append at the
list named in brackets. IDs are short: letters, numbers, `_` or `-`, starting
with a letter, unique in their list.

- `diagram` [`nodes`, then `edges`]: optional `direction` `vertical` (default)
  or `horizontal`. 1-16 nodes: `id`, `title` (100), optional `description`
  (280), `emphasis:true`, `items` (1-6 of `title` 80, optional `description`
  160) to group parts inside a node. Up to 24 edges: `from`, `to` (node IDs),
  optional `label` (80), `bidirectional:true`.
- `swimlane` [`lanes`, then `steps`]: 1-6 lanes (`id`, `title` 80), one per
  person, agent or app. 1-16 steps (`id`, `lane`, `title` 100, optional
  `description` 280) in execution order; each step connects to the next, so
  use a `diagram` for branches.
- `sequence` [`participants`, then `messages`]: 1-6 participants (`id`,
  `title` 80); 1-24 messages in order (`from`, `to`, `label` 120, optional
  `reply:true` for a dashed reply). Self-messages are fine.
- `chart` [`data`]: `style` `line` or `bar`, optional `unit` (40); 1-30 points
  (`label` 80, `value` a plain number, not formatted text). One series.
- `timeline` [`items`]: 1-20 events in the order given (`when` 80, `title`
  160, optional `detail` 1500).
- `cards` [`items`]: `layout` `list` (image beside text, default) or `grid`
  (image above text); 1-12 items (`title` 160, optional `description` 1000,
  `image`, `href` HTTP(S) link).
- `table` [`rows`]: `headers` (1-20, 80 each); 1-100 rows, each an array of
  strings or numbers with one cell per header.
- `details`: `body` (Markdown, up to 12,000), shown collapsed. To grow it,
  `replace` `/elements/visual/props/body` with the whole updated text. Keep the
  main answer outside it and do not nest `chat-ui` blocks.
- `file_changes` [`files`] (app only): 1-30 files (`path` 2048, optional
  `added`/`removed` measured line counts, `summary` 1000, `href`).
- `checks` [`checks`] (app only): 1-30 checks (`name` 160, `status` `passed`,
  `failed`, `running` or `not_run`, optional `detail` 4000 shown collapsed,
  `href`).
- `image` (app only, no title): `src`, optional `caption` (1000).

```chat-ui
{"op":"add","path":"/root","value":"visual"}
{"op":"add","path":"/elements/visual","value":{"type":"chart","props":{"title":"Requests per day","style":"bar","unit":"requests","data":[]},"children":[]}}
{"op":"add","path":"/elements/visual/props/data/-","value":{"label":"Mon","value":12}}
{"op":"add","path":"/elements/visual/props/data/-","value":{"label":"Tue","value":18}}
```

```chat-ui
{"op":"add","path":"/root","value":"visual"}
{"op":"add","path":"/elements/visual","value":{"type":"swimlane","props":{"title":"From request to result","lanes":[],"steps":[]},"children":[]}}
{"op":"add","path":"/elements/visual/props/lanes/-","value":{"id":"person","title":"Person"}}
{"op":"add","path":"/elements/visual/props/lanes/-","value":{"id":"agent","title":"AI teammate"}}
{"op":"add","path":"/elements/visual/props/steps/-","value":{"id":"ask","lane":"person","title":"Send request"}}
{"op":"add","path":"/elements/visual/props/steps/-","value":{"id":"prepare","lane":"agent","title":"Prepare the work"}}
```

The examples are fictional; use the person's real facts.

## Images

For a single image in app chat, prefer Markdown `![caption](Crew/example/assets/image.png)`
with an existing workspace path or HTTP(S) URL; no JSON is needed. Use angle
brackets for spaces: `![caption](<Crew/example/assets/my image.png>)`. Never use
an absolute computer path, an invented URL, a token or a data URL. Consecutive
Markdown images form a compact gallery; put prose between them to keep them
apart. People can enlarge and download them, and file permissions still apply.
Show screenshots, designs and other visual results inline without being asked.
By default copy an image into the project (for example `outputs/`) and show that
path so everyone reading the chat sees it. An absolute path from an attached
folder (`WORK_FOLDER_<ALIAS>`) shows only to people who attached that folder
themselves. For cards, use real image URLs or workspace paths, and leave
`image` out when there is none.

## Coding results

Use `file_changes` for files you actually changed and `checks` for build, test
or lint results you actually saw. Leave out line counts and links you do not
know; never write zero for an unknown count or invent a diff URL. Never imply a
check passed when it was not run: use `not_run` and say why in `detail`. A
successful build is not proof the live feature works. `href` is an existing
HTTP(S) link or an authorized workspace path. These widgets are snapshots, not
live controls; in Slack use a short Markdown summary instead.

## Slack

For a reply arriving through Slack, also read [Slack delivery](references/slack.md).
The same blocks become native Slack blocks; sending and access stay the
platform's job.
