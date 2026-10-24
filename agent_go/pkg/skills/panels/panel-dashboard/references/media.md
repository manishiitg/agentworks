# Live media and reviewable results

Use the shared `show_video`, `show_character`, `show_reference` and `show_document`
tools to publish saved project results to `ui_presentations` in `db/db.sqlite`.
These tools are available to Crew Builders and their existing function execution;
reading this guide grants no tools or additional access. External agents discover
and use the same admitted tools through `workspace` chat_tools/chat_tool.

Save dashboard-visible assets under `db/assets/`, using a stable subfolder for
each related job. Pass its stable `collection_id` to every presentation call.
The tools validate that the actual file exists; they write the presentation and
emit the existing project data-change notice immediately, during the agent turn.
Repeating a character name or file path updates its existing presentation revision.

`show_video` without `qa_report_path` is a review preview. To mark a final, supply
the passing `quality-report.json` for this exact file, with its required evidence
and inspected frames. A failed/missing report cannot mark a final. Do not describe
a preview as approved or manufacture a passing report. Optional source_url follows
the tool's supported CDN rules; the durable project file remains mandatory.

In React dashboards, export `async loadData(report)` and read the actual
`ui_presentations` schema. Return only the rows and resolved fields the page uses.
The host reruns loadData on project report notices, coalescing bursts; no custom
polling loop or chat-event subscription is needed. Ordinary shell writes are not
individual change notices. Use the managed tools for each result that must appear
while a job is still running. Static exports are snapshots, not live applications.

Import `MediaGallery`, `MediaPreview` and `UploadZone` from `@agentworks/dashboard`.
MediaGallery accepts items with id, kind, title, revision and parsed payload_json
as payload. Filter by payload.collection_id when displaying one job. Stable IDs
preserve the selected result and mounted video/audio player when unrelated results
arrive. A changed revision reloads that result. Keep actual final/preview status.

UploadZone is for signed-in Crew dashboards. It calls the existing Crew chat
attachment handler, with the current account's limits and server-selected folder;
it does not choose another person's folder or change sharing. Its onUploaded
callback receives {path, name, size, content_type}. The same operation is available
over MCP as dashboard action=upload (file_name, content_base64). Pass returned
paths to the project's existing chat or published function. These inputs can live
under uploads/ or shared/; the producer saves reviewable copies under db/assets/.
Never silently send a chat message or start paid work from loadData or a refresh.

Keep multi-minute work in the existing Crew function/background execution. Persist
provider job IDs, output paths and completed stages before polling or continuing.
On resume, inspect those receipts and recover the same submitted job before making
another paid call. Progress and media updates are stored project data; browser
refreshes must not start or restart work.
