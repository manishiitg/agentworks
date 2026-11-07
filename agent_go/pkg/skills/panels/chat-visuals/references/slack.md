# Chat visuals in Slack

Use the same json-render SpecStream chat-ui format documented in formats.md. The platform uses @json-render/chat and the official Slack adapter to present cards, charts and tables. Send the block in your normal reply; never post or edit Slack messages yourself to implement streaming.

Always put a useful written answer, conclusion and key numbers outside the visual. A missing formatter or rejected visual can then be delivered as plain text, with a readable notice; raw visual JSON is never the fallback. Ordinary Markdown code examples still work as code.

Native Slack chart limits are 20 points, 20 characters per label, 50 characters per title and two charts per message. Use short meaningful labels without changing values. Larger or unsupported charts become a readable text chart through the Slack adapter; do not upload data to an external chart image service. One native table per message, at most 100 rows and 20 columns; additional tables become readable text. Keep cells concise. The platform does not truncate your data merely to turn it into a native chart.

For a table, use type table, title, headers and rows. Rows are equal-width arrays of strings or finite numbers. Start with rows:[] then append complete rows at /elements/visual/props/rows/-. Example:

```chat-ui
{"op":"add","path":"/root","value":"visual"}
{"op":"add","path":"/elements/visual","value":{"type":"table","props":{"title":"Saved results","headers":["Day","Requests"],"rows":[]},"children":[]}}
{"op":"add","path":"/elements/visual/props/rows/-","value":["Monday",12]}
{"op":"add","path":"/elements/visual/props/rows/-","value":["Tuesday",18]}
```

Cards keep their text, links and HTTP(S) images. Workspace images remain private and are omitted in Slack; never make a file public just to display it. Timelines, sequences, swimlanes and diagrams become labeled tables and text when Slack has no matching visual. Expandable details appear as text because Slack has no equivalent expandable body. In-app chat keeps its existing presentation of these widgets.

Native content streams update progressively. Terminal-only sources wait for the completed reply; no terminal scraping is used. The platform throttles message edits and finishes with the saved reply as the authority. No new settings, tools or permissions are granted by these visual blocks.
