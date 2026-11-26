# Uploads and long-running work

UploadZone is for signed-in project dashboards. It calls the existing project chat
attachment handler, with the current account's limits and server-selected folder;
it does not choose another person's folder or change sharing. Its onUploaded
callback receives {path, name, size, content_type}. The same operation is available
over MCP as dashboard action=upload (file_name, content_base64). Pass returned
paths to the project's existing chat or published function. These inputs can live
under uploads/ or shared/; the producer saves reviewable copies under db/assets/.
Never silently send a chat message or start paid work from loadData or a refresh.

Keep multi-minute work in the existing project function/background execution. Persist
provider job IDs, output paths and completed stages before polling or continuing.
On resume, inspect those receipts and recover the same submitted job before making
another paid call. Progress and media updates are stored project data; browser
refreshes must not start or restart work.
