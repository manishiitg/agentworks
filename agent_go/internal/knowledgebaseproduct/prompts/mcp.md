Knowledge Base stores shared skills, facts, notes and sources in nested folders.
List accessible folders and entries, read a current version, and use an expected
version with a stable request ID to update or delete. update_knowledgebase accepts
a patch for large files. Save is immediately visible to permitted readers.
Git is explicit backup: commit selected versions, then push the returned receipt.
Keep request IDs and receipts for safe retries. Folder grants and connection caps
are checked for every operation, including push. Do not request repository keys.
