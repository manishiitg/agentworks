# Knowledge Base MVP Design

Status: Proposed implementation design based on the agreed product scope.

Date: 2026-10-04

## 1. Product intent

Build a shared knowledge base that Crews, Code, Workflows, and external local agents such as Claude Code can read and update through MCP.

The application is a content viewer with a builder chat for basic access management. It has no content editor. Entries live in nested folders, and access granted on a folder applies to its descendants.

The live knowledge base is the source of truth. A private Git repository is a versioned content backup. Writers explicitly commit and push through MCP; saving content does not depend on Git.

## 2. Agreed MVP scope

| Area | MVP behavior |
| --- | --- |
| Content | Skills, notes, facts, and sources use one common entry model. |
| Organization | Nested folders, for example `Engineering/Payments/Checkout`. |
| Application | Browse folders, search, and read entries; display relevant activity and backup status. |
| Builder chat | View access, grant or revoke access, and change basic roles. |
| MCP clients | Crews, Code, Workflows, and external local agents use the same backend. |
| Content mutations | Create, update, patch, and delete through MCP. |
| Access | Authenticated users and service accounts receive inherited folder permissions. |
| Large files | Read a section or line range and submit a unified diff through `update_knowledgebase`. |
| Visibility | Successful writes are immediately readable by authorized users. |
| Backup | Writers explicitly commit selected content snapshots and push them through MCP. |
| Backup format | Plain Markdown files in the same folder hierarchy, without embedded metadata. |
| Concurrency | An internal version token prevents overwriting concurrent changes. |
| Accountability | Record content changes, access changes, and backup operations. |

## 3. Content model

All content types share the same storage, permissions, search, read, and update behavior. Type is a label in the live system, not a separate subsystem.

| Type | Purpose | Example |
| --- | --- | --- |
| `skill` | Instructions for performing a task. | Deployment and rollback procedure. |
| `note` | Observations, ideas, or a record of discussion. | Architecture meeting notes. |
| `fact` | A specific statement about something. | Production uses PostgreSQL. |
| `source` | Original evidence or a reference to it. | A design document URL or pasted transcript. |

Each entry is a Markdown file on disk with a filename, title, type, and optional description and tags. Internal IDs and folder IDs resolve to stable paths while moves and renames are deferred. Markdown content is never stored as database rows.

The version token is opaque to clients. Internally, each entry has a monotonically increasing change sequence. Content edits, metadata edits, and deletion all advance this sequence; the server also records the sequence of the last content or existence change. Clients compare returned tokens for equality and must not infer ordering from their spelling.

Title, description, tags, and type can be edited through MCP. Metadata-only updates advance the entry version but do not change its Markdown bytes or make its Git content backup pending. Backup status compares live content with the content actually present at the tracked remote branch tip, not with any older commit in history.

Permissions stay in the sqlite grant store. Entry metadata and change sequences live in per-folder registry files alongside the Markdown; version tokens are derived from content hashes plus the registry sequence. None of this is inserted into the Git backup, which carries plain Markdown only. Users can include ordinary links or citations in their content.

For MVP, a source is a URL saved as content or pasted original text. The system does not automatically fetch, ingest, extract facts from, or enrich sources.

## 4. Nested folders

```text
Organization
└── Engineering
    └── Payments
        ├── Checkout
        │   ├── deployment.md          [Skill]
        │   ├── architecture-notes.md  [Note]
        │   ├── production-database.md [Fact]
        │   └── design-document.md     [Source]
        └── Billing
            └── invoice-processing.md [Skill]
```

Folders are generic containers. Engineering, Payments, and Checkout do not require separate department, pod, or service entity types.

- Each entry belongs to one folder.
- Each folder has one parent, except the organization root.
- Folder IDs are stable; paths are human-readable locators.
- Folder names and entry filenames share a sibling namespace and are unique within their parent under the name policy below.
- Folder creation under an existing scope is available through MCP to authorized writers.
- The organization root and initial administrator are created during provisioning.
- Folder deletion, including empty folders, is deferred. Recursive deletion is not available in MVP.
- Folder and entry moves and renames are deferred because they change inherited access or Git paths. Entry metadata edits do not rename its file or move it.

### 4.1 Name and path policy

Use a deliberately restricted name policy for predictable Git exports:

- A folder name or filename stem contains 1–64 ASCII letters, digits, spaces, underscores, or hyphens; its first character must be a letter or digit, and it cannot end in a space.
- Entry filenames end in the lowercase extension `.md`. Display titles can contain Unicode and are independent of filenames.
- Reject `.` and `..`, `.git`, separators inside a name, control characters, and Windows device names such as `CON`, `NUL`, `AUX`, `PRN`, `COM1`–`COM9`, and `LPT1`–`LPT9`, including filename stems with those names.
- Enforce case-insensitive sibling uniqueness: `Payments` and `payments` cannot coexist. Path lookup uses the same case-insensitive key and returns the stored display spelling.
- Organization-relative paths use `/`, have no empty components, and are at most 1,024 ASCII bytes. Reject absolute paths and traversal rather than silently rewriting them.
- Export regular Markdown files only. Symlinks and executable files are not content types.

Git operations pass validated paths as arguments with option boundaries; they do not interpolate client input into shell commands.

## 5. Identities and access controls

### 5.1 Caller identities

Every app session and MCP connection resolves to an authenticated identity in an organization.

| Caller | Identity |
| --- | --- |
| Priya using the app | Priya's user identity. |
| Priya using local Claude Code | A connection acting on behalf of Priya. |
| A shared Crew | A dedicated service account. |
| An automated workflow | A dedicated service account, such as `deployment-workflow`. |
| Code acting on behalf of a user | That user's delegated identity, optionally narrowed to specific folders. |

For MVP, use separate revocable MCP tokens for users or service accounts. Tokens resolve to server-side identities and grants; callers cannot choose an arbitrary user ID in tool arguments. Existing product identity can be reused for first-party clients.

Do not use one shared organization token. A connection may narrow an identity's access but must never expand it. Git credentials are held separately by the backend and are never returned to MCP clients.

Connection narrowing is server-side token configuration, not a permission supplied with each tool call. Its optional `folder_scopes` field is either `null` (the identity's current content permissions) or a list such as:

```json
{
  "folder_scopes": [
    { "folder_id": "folder_checkout", "max_role": "Editor" }
  ]
}
```

Each scope covers that folder and its descendants. An empty list allows no content access. At token issuance, verify that each folder is in the same organization and that the requested Reader/Editor cap does not exceed the target identity's current role there. Only the user for their own identity or an organization administrator may issue such a token.

At every call, intersect the identity's current effective permissions with these stored caps. Overlapping connection scopes use the highest applicable cap, but never exceed the identity's current role. Subsequent revocation or downgrade therefore takes effect without reissuing the token. MCP content tokens do not enable the builder chat's access-management actions.

### 5.2 Folder roles

| Role | Allowed operations in the granted folder and descendants |
| --- | --- |
| Reader | Browse, search, and read content. |
| Editor | Reader operations plus create, update, patch, delete, and explicitly back up permitted content. |
| Owner | Editor operations plus manage access. |

Organization administrators manage organization membership, service accounts, connection credentials, and Git backup configuration. They can manage access throughout the organization.

Grants are additive and inherit downward. The effective role is the highest role among grants on the folder and its ancestors: `Owner > Editor > Reader > no access`, before applying any connection cap. Organization administrators have Owner authority throughout the organization. The MVP has no explicit deny rules or inheritance exceptions. An Editor grant on Payments remains effective in Checkout even if Checkout also has a Reader grant.

Examples:

- Priya is Reader on `Engineering/Payments`: she can read Checkout and Billing, including future entries and subfolders.
- Priya is Reader only on `Engineering/Payments/Checkout`: she can read Checkout but not Billing or unrelated Engineering content.
- The deployment workflow is Editor on Checkout: it can change and back up Checkout entries but cannot modify Billing.
- Removing a direct grant removes only that grant. Other direct or inherited grants may still provide access.

New folders and entries inherit the parent folder's grants immediately. Inheritance is evaluated against the current ancestor grants at authorization time; grants are not copied into new child rows. Parent grant changes affect existing descendants. An Editor creating a child folder does not gain permission to manage access automatically. Per-entry permissions and restricted exceptions within a shared folder are deferred; use separate folders for separate access boundaries.

### 5.3 Enforcement

```text
Credential → Identity → Organization → Effective folder grants → Operation
```

The server checks authorization on every operation, including listing, searching, reading, patching, deleting, committing, and pushing. The same policy applies to the app and all MCP clients. Folder grants are stored in a server-owned sqlite database outside the workspace, so content writers can never modify permissions through file tools. Sqlite holds access control only: no content, metadata, versions, or activity.

- Respect existing platform account status, read-only restrictions, product entitlements, and connection capabilities before evaluating folder grants. A folder role cannot override a disabled account or a stricter platform/connection boundary.
- Search and folder listing return only accessible content.
- A known entry ID or path does not bypass permission checks.
- Access to a child allows only the ancestor labels needed to navigate to it; it does not expose sibling folders or their contents.
- Paths must stay within the organization and authorized folder. Reject traversal, absolute host paths, and patch headers targeting another file.
- Revocation takes effect on subsequent operations, including push of an already prepared commit.
- Tool results and error details must not expose inaccessible content.
- A resource that does not exist and a resource the caller cannot read return the same `NOT_FOUND` code, message, and response shape, with no existence hint. This applies to entry/folder lookup, filtered searches, activity, and backup receipts. If the caller can read an entry but lacks permission to mutate it, `FORBIDDEN` is appropriate.
- Recheck current authorization before returning a cached idempotent result; a revoked caller must not recover protected content through retries.

## 6. Application and builder chat

The application provides a nested folder browser, basic keyword search, type/tag filters, and a Markdown reader. Entries show their title, type, attribution, last updated time, and backup status.

There are no create, edit, patch, or delete controls for content in the application. Content changes happen through MCP.

The builder chat is limited to access management. Examples:

- "Give Priya read access to the Payments folder."
- "Give the deployment workflow update access to Checkout."
- "Who has access to Billing?"
- "Remove Priya's direct access to Checkout."

The chat resolves the folder and identity, displays the applied change, and invokes dedicated access-management backend actions. Those actions independently check the signed-in user's authority. Resolve ambiguous names before changing access.

The builder chat cannot create or edit entry content, patch files, or initiate Git backups. Readers can inspect their own effective access; Owners and administrators can inspect and manage grants within their authority.

## 7. MCP interface

These are proposed public names. They can be routed through the existing MCPBridge while sharing the underlying knowledge-base service.

| Tool | Purpose | Required access |
| --- | --- | --- |
| `list_knowledgebase` | Browse like a filesystem: list folders and entry metadata by path, with depth, glob, and pagination. | Reader |
| `search_knowledgebase` | Keyword search within accessible content; optional folder, type, and tag filters. | Reader |
| `read_knowledgebase` | Read a full entry, heading section, or line range; return its current version. | Reader |
| `create_knowledgebase_folder` | Create a child folder that inherits parent access. | Editor on parent |
| `create_knowledgebase` | Create an entry with filename, type, title, content, and optional description and tags. | Editor on folder |
| `update_knowledgebase` | Patch or replace content and/or edit metadata, with a required expected version. | Editor |
| `delete_knowledgebase` | Delete an entry with a required expected version; return a deletion token for backup. | Editor |
| `commit_knowledgebase` | Prepare a Git commit from explicitly selected permitted content versions. | Editor on every selected path |
| `push_knowledgebase` | Push the prepared commit using backend-held Git credentials. | Editor on every included path |
| `get_knowledgebase_backup_status` | Show live, committed, and pushed versions for accessible content. | Reader |

All operations are scoped to the authenticated organization. Content operations resolve an entry by ID or organization-relative path. Types use the same tools; no separate tool families are needed for skills, notes, facts, or sources.

Mutating calls require a request ID for safe retries. Deduplicate by organization, initiating identity, tool name, and request ID, storing a hash of the normalized arguments and the outcome for seven days. Repeating a request with the same arguments returns the existing outcome without repeating its effect. Reusing its ID with different arguments returns `REQUEST_ID_REUSE`. While the original call is in progress, a retry returns `REQUEST_IN_PROGRESS` with a suggested retry delay.

Automatic retry guarantees expire after seven days. Beyond that window, clients must inspect current state before issuing a new operation. Backup receipt state provides an additional durable check against pushing the same commit twice. Transient failures with no effect are retryable using the same ID; a new commit preparation after a stale branch conflict uses a new ID.

Results use structured error codes, safe messages, and a `retryable` flag. Details may include the current version of an entry the caller can read, but never an inaccessible path, raw repository-wide log, or Git credential.

### 7.1 Large-file reads and patches

The preferred update mode is a unified diff against the version the client has read. Full replacement is available when the caller intentionally replaces an entry.

Read locators specify exactly one of `entry_id` or `path`. A read uses exactly one mode: the whole entry (no selector), a line range, or a heading section.

- Line ranges are 1-indexed with an inclusive end. Both `start_line` and `end_line` are required, and `1 <= start_line <= end_line`. Clamp an end beyond EOF to the final line; reject a start beyond EOF with `RANGE_OUT_OF_BOUNDS`. Empty content has zero lines and is readable in whole-entry mode.
- A heading selector is `section: {"heading": "Rollback", "occurrence": 1}`. Match the heading's plain text exactly and case-sensitively using a Markdown parser; headings inside code blocks do not count. Include the heading and content until the next heading of the same or higher level, or EOF.
- `occurrence` is 1-indexed in document order. It is optional for a unique heading and required when headings repeat; otherwise return `AMBIGUOUS_SECTION`. An absent heading or occurrence returns `SECTION_NOT_FOUND` for an accessible entry.
- Mixing a section selector with line bounds returns `INVALID_ARGUMENT`.

Example partial read:

```json
{
  "path": "Engineering/Payments/Checkout/deployment.md",
  "start_line": 120,
  "end_line": 145
}
```

Return the requested text, actual line bounds, total line count, and an opaque version token. That token applies to the whole entry, including when only a section was read.

Example patch:

```json
{
  "path": "Engineering/Payments/Checkout/deployment.md",
  "expected_version": "v12",
  "diff": "--- a/deployment.md\n+++ b/deployment.md\n@@ -120,1 +120,2 @@\n Run the deployment command.\n+Verify service health before continuing.\n",
  "request_id": "checkout-health-step-001"
}
```

The server:

1. Resolves identity, organization, entry, and effective write access.
2. Confirms that the expected version matches the current version.
3. Validates that the diff targets only the resolved entry.
4. Applies and validates all hunks against a temporary copy.
5. Atomically saves the complete result only if the version still matches.
6. Records attribution and returns the new version and backup status.

If any hunk fails, no content changes are saved. A concurrent update returns a version conflict; the client reads the relevant current text and prepares a new diff. The MVP does not silently merge conflicting writes or permit force-overwrite updates.

### 7.2 Creation, metadata updates, and limits

`create_knowledgebase` requires exactly one parent locator (`folder_id` or `folder_path`), plus `filename`, `type`, `title`, `content`, and `request_id`. Description defaults to an empty string and tags to an empty list. Creating a folder similarly requires one parent locator, a name, and a request ID. Uniqueness checks and creation happen atomically.

`update_knowledgebase` accepts a locator, `expected_version`, `request_id`, and:

- At most one of `diff` or replacement `content`; providing both returns `INVALID_ARGUMENT`.
- An optional `metadata` object containing only `title`, `description`, `tags`, or `type`.
- At least one content mode or a non-empty metadata object. Metadata-only changes are supported.

Omitted metadata fields are unchanged. An empty description clears it; an empty tag list clears tags. Reject null values, unknown fields, and invalid types. Content and metadata in one call are saved atomically against the same expected version. A no-op returns the unchanged version and `changed: false`.

Example metadata-only update:

```json
{
  "path": "Engineering/Payments/Checkout/deployment.md",
  "expected_version": "v13",
  "metadata": {
    "title": "Checkout deployment and rollback",
    "tags": ["deployment", "checkout"]
  },
  "request_id": "checkout-title-001"
}
```

MVP limits, measured after JSON decoding:

| Field or operation | Limit |
| --- | --- |
| Content | 10 MiB of UTF-8 text; reject binary data and NUL bytes. |
| Diff | 2 MiB of UTF-8 text, targeting one existing entry. |
| Resulting patched content | The same 10 MiB content limit. |
| Title | 1–200 Unicode characters. |
| Description | At most 4,000 Unicode characters. |
| Tags | At most 50 unique non-empty tags, each at most 64 characters. |
| Type | Exactly `skill`, `note`, `fact`, or `source`. |
| List/search page | Default 50 results, maximum 100; opaque cursor. |
| Commit selection | 1–100 total entries and deletions, without duplicate target paths. |
| Commit message | 1–2,000 Unicode characters. |
| Request ID | 1–128 ASCII letters, digits, underscores, or hyphens. |

Whole-entry reads can return up to the content limit. Clients should use sections or line ranges for large entries rather than relying on silent response truncation. Store UTF-8 text with LF line endings so reads and generated diffs use the same representation. Validate limits and normalize replacement text before saving; patch context is matched against the stored LF text. Initial limits may be adjusted operationally, but must be advertised in the tool contracts.

### 7.3 Reuse of existing MCPBridge patch functionality

Expose the existing diff patch capability as `update_knowledgebase`, with a knowledge-base-specific description and schema. Users do not need to see `diff_patch_workspace_file` on this product's MCP surface.

The existing workspace client accepts `filepath` and `diff` and includes folder write-path validation. The knowledge-base adapter maps the public entry locator to an authorized target and reuses the underlying patch implementation.

The adapter must add knowledge-base identity and folder authorization, required version checks, atomic persistence, and activity recording. Renaming the tool alone does not provide these guarantees. Keep the general workspace tool available to its existing consumers.

Relevant existing code inspected during planning:

- `mcp-agent-builder-go-connect-ui/agent_go/pkg/workspace/diff_patch_workspace_file.go`
- `crew-session-cache/workspace/handlers/diff_patch.go`
- `mcpagent/cmd/mcpbridge/main.go`

These paths establish a reuse candidate, not a decision to implement the product in one of those checkouts. Verify the chosen repository's patch implementation before wiring it in.

## 8. Immediate shared visibility

```text
Client → MCP → Authentication and folder permissions → Live knowledge base
                                                        ↓
                                             App and other MCP readers
```

A successful create, update, or patch is published to the live knowledge base immediately. There is no private draft or Git staging requirement before other authorized users can read it.

Example:

1. A writer patches `Checkout/deployment.md` through MCP.
2. The server saves version `v13` and reports success.
3. Priya has Reader access to Checkout and can read `v13` immediately.
4. The writer has not committed or pushed. The entry is still readable and its backup status is pending.

Content writes invalidate the relevant reader and search caches. Reads following a successful write must return the current content; search must not continue serving an obsolete cached version.

Unpushed changes are shared saved content. A Git commit is a backup snapshot, not the publishing step.

## 9. Explicit Git backup through MCP

### 9.1 Backup repository

Use a private Git repository, including GitHub or another supported Git remote. For MVP, configure one repository and one backup branch per organization.

Mirror folder paths and store plain Markdown:

```text
Engineering/
└── Payments/
    └── Checkout/
        ├── deployment.md
        ├── architecture-notes.md
        ├── production-database.md
        └── design-document.md
```

Do not inject YAML frontmatter, entry IDs, revision numbers, access grants, or metadata manifests. Git provides version history for backed-up content. Internal live version tokens serve concurrency checks and do not duplicate Git metadata in files.

### 9.2 Commit and push flow

```text
Save through MCP → Immediately shared live content
                           ↓ explicit writer call
                   commit_knowledgebase
                           ↓ explicit writer call
                    push_knowledgebase
                           ↓
                    Private Git remote
```

There is no background agent committing or pushing. A Crew or workflow can be instructed to call these tools itself after writing; that is an explicit client action.

`commit_knowledgebase` takes an `entries` list of locators and expected versions, an optional `deletions` list of deletion tokens, a commit message, and a request ID. Either list may be omitted, but the combined selection must be non-empty. Resolve and authorize the entire selection, then capture all selected versions in one consistent snapshot of the selected files and registry state. Any stale version or unauthorized selection rejects the whole request.

The backend prepares a commit with the current tracked remote branch tip as its parent. Its complete Git tree carries every unselected path forward unchanged from that parent. Only the selected additions, modifications, and deletions appear in its diff. Do not create a tree consisting only of selected files, which would delete the rest of the repository.

Authorization is checked on every changed path, not on unchanged files carried forward in the tree. The backend's repository credential has organization-wide authority; the user's authority remains folder-scoped. Return only the selected file information, not repository-wide trees, history, commit messages, or command output that could disclose other folders.

The receipt includes `receipt_id`, base commit ID, prepared commit ID, selected entry/deletion versions, creation and expiry times, and state. Git commit IDs are opaque identifiers, not permission to read their trees. If the selection produces no Git changes, return `NO_CHANGES` as a successful no-op without creating a commit or push receipt; confirm the selected content/absence already matches the tracked remote state.

Example:

```json
{
  "entries": [
    {
      "path": "Engineering/Payments/Checkout/deployment.md",
      "expected_version": "v13"
    }
  ],
  "message": "Add deployment health verification",
  "request_id": "checkout-backup-001"
}
```

`push_knowledgebase` takes `receipt_id` and `request_id`. Only the initiating identity can push its receipt. Recheck current Editor permission and connection scope for every selected change, including a deletion's retained folder. The receipt must belong to the same organization and must not be expired or stale.

Do not stage an entire shared working directory. Use an isolated index or staging area populated from the published base, then overlay only the selected immutable snapshots. Unrelated users' unpublished commits must not become ancestors of a push.

A later commit may have another user's already-pushed commit as its ancestor, including changes in a different folder. This is expected on a shared branch and does not grant access to those changes. Only unpublished cross-user changes are excluded from the new operation.

Before publication, reject a snapshot of an entry that has since been deleted or replaced by a new entry at that path. Also reject a content snapshot older than that entry's already-published content-change sequence with `BACKUP_VERSION_CONFLICT`; do not regress a newer backup. A newer live version that has not yet been backed up does not invalidate an earlier prepared content snapshot.

Because live content is shared, a selected entry snapshot may contain edits by multiple contributors. Record both the contributors in the activity history and the identity initiating backup. The commit receipt shows exactly which entry versions will be backed up.

If content changes after commit, pushing the earlier snapshot is allowed, but the newer live version remains pending. A push must never mark a newer version as backed up when that version was not included.

### 9.3 Concurrent publication

Use one shared publication lock per organization/repository/branch across server processes. Preparation briefly acquires the same lock to reconcile remote state and pin a published base, then releases it; it does not hold this lock through the user's later push. A prepared commit may therefore become stale, which is an ordinary explicit retry case. Never hold the lock while waiting for a user to initiate push.

An explicit push call:

1. Validates the caller and receipt, then tries to acquire the publication lock.
2. If busy, returns `BACKUP_BUSY` with `retryable: true` and `retry_after_seconds: 3`; there is no unbounded server queue.
3. Reconciles any previous push with an unknown outcome before attempting another publication.
4. Reads the remote tip and compares it with the receipt's base. If different, marks the receipt `STALE` and returns `BACKUP_BRANCH_ADVANCED`.
5. Revalidates included path permissions and entry/deletion generations immediately before publication.
6. Durably records `PUSH_UNKNOWN` before contacting Git, then conditionally publishes only the prepared commit against the exact expected remote base. First prove the prepared commit has that base as its direct parent (or is a root commit for an empty branch). Git's exact ref lease then provides compare-and-swap, including protection against a concurrent branch deletion or rewind. This permits only the proven fast-forward; it never authorizes a history rewrite. The pre-recorded state makes process failure recoverable. Never automatically rebase, merge, rebuild, or import remote content.
7. On confirmed success, records the receipt state in the staging repo's Git refs, then releases the lock. Path backup state is derived from the new tip, not stored separately.

Example: two writers prepare from `C0`. The first pushes Checkout changes as `C1`. The second writer's Billing receipt based on `C0` is now stale. They explicitly prepare a new Billing commit based on `C1`, preserving Checkout unchanged, and push it. The second writer does not need access to Checkout.

Expected pushes by other users and unexpected direct repository changes both require a new preparation. The exact expected-base ref lease rejects any branch change during publication, even a rewind that would still allow an ordinary fast-forward. Repository changes outside this adapter require administrator reconciliation before the adapter trusts their file state; a conflict does not import them into the live knowledge base.

### 9.4 Deletion backups and path reuse

`delete_knowledgebase` atomically removes the entry from live listing/search/read results and retains a tombstone containing its entry ID, folder ID, path, deletion sequence, actor, and a stable opaque `deletion_id`. Its idempotent response returns that deletion ID. Normal readers cannot read the removed content through the deletion record.

Select a deletion independently of a live version:

```json
{
  "deletions": [
    { "deletion_id": "deletion_checkout_guide_001" }
  ],
  "message": "Remove obsolete checkout guide",
  "request_id": "checkout-delete-backup-001"
}
```

Resolve the token to its server-side path and retained folder; a client-supplied path cannot change what it deletes. Both preparation and push check the caller's current Editor permission there. `get_knowledgebase_backup_status` can include authorized deletion records so another permitted writer can find and back up an unpushed deletion.

Keep tombstones and their tokens as durable internal records. Do not export them to Git. Reserve the deleted path until its absence is confirmed in the remote and no unknown push can restore it. It can then be reused by a new entry with a new ID. Prepared snapshots are bound to entry IDs as well as paths, so an old content or deletion receipt cannot overwrite or remove a replacement entry. A retry of an already-pushed receipt returns its recorded outcome without touching the replacement.

### 9.5 Receipt lifecycle and retries

- A prepared receipt expires 24 hours after creation if it has not been pushed. Expiry discards staging objects when safe, without changing live content; the writer prepares a new commit.
- States are `PREPARED`, `PUSH_UNKNOWN`, `PUSHED`, `STALE`, and `EXPIRED`. A failure confirmed to have made no remote change returns an unexpired receipt to `PREPARED` with failure details and can be retried. Uncertain delivery remains `PUSH_UNKNOWN`.
- Keep terminal receipt outcomes for at least 30 days. An authorized retry of a `PUSHED` receipt returns the original outcome and performs no Git write, even if its prepared-snapshot expiry time has passed.
- Unknown or another identity's receipts return `NOT_FOUND`. An owned expired receipt returns `SNAPSHOT_EXPIRED`; a stale receipt requires explicit new preparation.
- Multiple unexpired receipts may select the same entry. Each preserves its own immutable content. Branch advancement, entry generation checks, and the published-sequence check prevent an older receipt from overwriting a newer backup.
- If a push times out or the process fails before recording success, set or recover `PUSH_UNKNOWN`. The next explicit push call checks whether the prepared commit reached the remote. Record an observed success without pushing again. If it did not reach the remote and the base still matches, the original receipt can be retried.
- Do not expire an unknown outcome or permit another publication until it has been reconciled. If the remote is unavailable, return a retryable backup error; live reads and writes remain available. Reconciliation only observes the remote and records state; it does not commit or push autonomously.

### 9.6 Backup status and failures

Track current entry version, current content fingerprint, prepared snapshots, and the selected path's latest confirmed remote fingerprint/existence. Internally retain content-change sequences to detect attempts to publish older content. Equality with the current remote fingerprint is sufficient to determine whether live bytes are backed up; clients do not need ordered public tokens.

Show an aggregate only for content the caller can access:

- **Pending:** current content or deletion is different from the confirmed remote state and has no matching valid prepared receipt.
- **Committed, not pushed:** a matching prepared snapshot exists but has not reached the remote. A receipt of an older version does not make newer content committed.
- **Backed up:** current live bytes/existence match the latest confirmed remote state for that path. A matching older historical commit alone is insufficient.

Expose `last_backup_error`, `last_successful_backup_at`, and any unknown/stale receipt as separate details rather than replacing content state with a generic failure label. Metadata-only changes do not alter content backup state. A later deletion remains pending even if an older version of its content exists in Git history.

Commit and push calls can be retried safely using their request IDs and receipts. Network, credential, and Git failures never undo a successful content write or block readers from using live content.

### 9.7 Backup access and recovery limits

Application folder permissions do not apply inside the Git repository. Anyone granted direct repository access may read the entire exported content. Restrict repository access to backup administrators; normal readers and writers use the app and MCP.

Git backs up published content and non-empty folder paths. It does not back up identities, permissions, entry metadata, empty folders, activity, configuration, or edits that have not been pushed. Infrastructure backups must include the complete live Markdown tree, per-folder registries, private identity/request/journal/activity/receipt state, the isolated staging repository, and a consistent SQLite grant-store snapshot. File backup is infrastructure recovery, not an automatic Git publishing agent.

Recovery must preserve folder grants and metadata from the grant-store and registry-file backups. If only Markdown files are available, restore them into an administrator-only area until access has been assigned; do not make recovered files broadly readable by default. A self-service restore interface is deferred.

## 10. Backend boundaries and records

```text
App viewer ───────────────┐
                         ├─→ Knowledge-base service → Live files + sqlite grants
MCP clients → MCPBridge ─┘              │
                                       ├─→ Activity records
Builder chat → Access actions ──────────┤
                                       └─→ Git backup adapter → Private remote
```

The shared service owns authorization and live persistence. MCPBridge exposes the tools, and the Git adapter handles only explicit backup operations. Patch processing produces new content for the shared service to save; it does not bypass that service with unrestricted host filesystem writes.

Minimum internal records:

| Record | Main responsibility |
| --- | --- |
| Organization | Root boundary, initial administrator, backup configuration. |
| Identity | User or service account. |
| MCP connection/token | Authenticated identity, optional narrower scope, revocation. |
| Folder | Directories on disk; path is stable while moves/renames are deferred; ACL generation is a persistent security counter. |
| Folder grant | Identity, folder, and Reader/Editor/Owner role, plus the ACL generation counter; SQLite outside workspace-docs. |
| Entry | Markdown file on disk; metadata and change sequence in the per-folder registry file. |
| Deletion tombstone | Recorded in the per-folder registry file: stable token, original path, sequence, attribution. |
| Change/activity | Permission-filtered private activity records plus existing tool-call logs and Git history; preserve deletion attribution. |
| Mutation request | Domain request IDs and durable outcomes in the private file journal; seven-day retry retention. The existing workflow-specific submission ledger is not a drop-in content transaction. |
| Backup snapshot/receipt | Prepared Git commit anchored in the private staging repository, with an opaque receipt ID and private ownership/selection/version/expiry/state record. A commit SHA alone is insufficient. |
| Backup path state | Derived by comparing live bytes against the tracked remote branch tip. |
| Publication lock | File lock (flock) on the staging repo; safe across server processes. |

Keep credentials out of content, tool responses, exported files, and activity logs. Activity displays follow the same content access boundaries.

Each file mutation takes a process-safe lock and durably journals its complete content, registry, activity, and retry-outcome changes before applying them. Recovery completes an interrupted transaction before any content read or write. Readers take the corresponding lock, so they cannot observe the middle of a multi-file save. Atomic rename is one step of this protocol; it does not by itself make a content file and its registry a single transaction.

Private registries, journals, identities, request outcomes and backup receipts are never content targets or search results. The entire data root is outside general workspace file roots. Folder authorization runs before returning entries or consuming a page; opaque cursors bind the query and principal scope and recheck current grants.

## 11. Failure behavior

| Condition | Required behavior |
| --- | --- |
| Invalid or revoked MCP credential | Reject before accessing content. |
| Missing target or target without read access | Identical `NOT_FOUND` response; disclose neither existence nor protected content. |
| Readable target without required write/Owner access | `FORBIDDEN`; no mutation. |
| Filename or folder collision on create | `NAME_CONFLICT` after authorizing the parent; no overwrite. |
| Invalid name, path, type, selector, or both diff and replacement | `INVALID_ARGUMENT`; no mutation. |
| Content, diff, result, or other field exceeds its limit | `LIMIT_EXCEEDED`; no mutation. |
| Line range starts beyond EOF | `RANGE_OUT_OF_BOUNDS`; no silently empty success. |
| Heading missing or ambiguous | `SECTION_NOT_FOUND` or `AMBIGUOUS_SECTION`, only for an accessible entry. |
| Expected version is stale | `VERSION_CONFLICT`; leave content and metadata unchanged. |
| Diff is invalid or any hunk fails | `PATCH_FAILED`; reject the entire patch and accompanying metadata edits. |
| Duplicate mutation request | Return the existing outcome; do not apply twice. |
| Request ID reused with different arguments | `REQUEST_ID_REUSE`; no mutation. |
| Original request still running | `REQUEST_IN_PROGRESS` with a retry delay. |
| Selected commit version changed | `VERSION_CONFLICT`; reject the entire preparation. |
| Access revoked before push | Reject push of that snapshot. |
| Deletion path not yet safe to reuse | `PATH_PENDING_DELETION_BACKUP`; create nothing. |
| Unknown, foreign, or inaccessible receipt/deletion token | `NOT_FOUND`; no existence hint. |
| Owned expired receipt | `SNAPSHOT_EXPIRED`; prepare a new commit. |
| Already-pushed receipt | Return its recorded success without repushing. |
| Publication lock busy | `BACKUP_BUSY`, retryable, with a three-second suggested retry delay. |
| Backup snapshot would restore a deleted/replaced entry or regress a published version | `BACKUP_VERSION_CONFLICT`; no publication. |
| Git remote unavailable | `BACKUP_UNAVAILABLE`; keep live content available and preserve retry state. |
| Push outcome uncertain | `BACKUP_OUTCOME_UNKNOWN`; reconcile before further publication. |
| Remote branch advanced or diverged | `BACKUP_BRANCH_ADVANCED`; no force overwrite, automatic rebase, or merge. |
| Unexpected external repository changes | `BACKUP_REMOTE_CHANGED`; administrator reconciliation required before preparation. |

## 12. MVP acceptance criteria

1. Priya granted Reader on Payments can browse and read Checkout and Billing through both app and MCP, but cannot update either.
2. Priya granted Reader only on Checkout cannot discover Billing entries through listing, search, reads, activity, or backup results.
3. A workflow with Editor on Checkout can create and patch entries there and cannot modify Billing.
4. A saved update is readable by Priya before any commit or push.
5. Two agents editing the same version cannot silently overwrite one another; the later conflicting write is rejected.
6. A partial read of a large entry supplies enough line and version information for a scoped diff update.
7. A failed multi-hunk patch leaves the entire entry unchanged.
8. The knowledge-base MCP surface exposes `update_knowledgebase` backed by the reused patch functionality and knowledge-base safeguards.
9. A writer can explicitly commit and push permitted versions without receiving direct repository credentials.
10. A backup commit changes only the selected authorized paths, preserves all unselected paths from its published parent, and stores plain Markdown content.
11. New edits made after a snapshot remain marked pending after that snapshot is pushed.
12. Revoking access prevents subsequent reads, writes, and pushes within the revoked scope.
13. Git failures do not affect the availability of already saved content.
14. Builder chat can manage authorized folder grants and cannot edit content.
15. Restoring the grant store, registry files, and Git content preserves the distinction between live permissions and repository access.
16. Two writers backing up different folders from the same base produce one successful push and one explicit stale-receipt conflict. A new preparation preserves the first writer's published files without exposing them.
17. Deleting an entry returns a token that can be backed up after the live entry is gone, with folder authorization checked again at push.
18. Metadata-only updates are version checked and immediately visible, while Markdown backup state stays unchanged.
19. Inheritance uses current ancestor grants; changing a parent grant changes descendant access without copied grant rows.
20. Connection caps cannot expand identity access, including after a parent grant is revoked.
21. Retrying after an uncertain successful push cannot create another commit, regress content, or mark a later live version backed up.
22. Missing and unreadable resources have identical lookup error contracts; known readable resources may return a write-permission denial.
23. Case-insensitive name collisions, invalid paths, mixed selectors, and mixed replacement/diff requests are rejected before changing state.
24. The registered product uses the shared viewer/chat shell and platform identity. Its access-builder principal cannot execute content or backup mutations, even when the signed-in user separately has Editor access through content MCP.

## 13. V2 and deferred work

- Knowledge graphs and structured relationships between entries.
- Automated ingestion, fact extraction, enrichment, and consolidation.
- Semantic retrieval, synthesized answers, and broader research capabilities.
- Two-way Git synchronization and imports of repository edits.
- Branches, pull requests, review/approval workflows, and automatic merge resolution.
- Folder deletion, entry/folder moves and renames, inheritance exceptions, explicit deny rules, and per-entry access overrides.
- Group-based permission management and advanced identity provisioning.
- Automated Git backup schedules and a self-service restore interface.

## 14. Implementation in the existing product platform

Implement Knowledge Base as another built-in product in the AgentWorks repository, with product ID and surface ID `knowledgebase`. Reuse the existing application shell, authentication, account directory, agent-profile runtime, conversation lifecycle, and MCP transports. Add the knowledge-base domain service and its viewer/access components within that platform.

### 14.1 What exists and what must be added

The inspected repository uses the filename `product.yaml`, with `schema_version: 2`, rather than `product.yml`. Existing manifest-backed products load embedded YAML through `agentprofiles.LoadProductManifest`. The shared schema supports profile tools, runtime policies, branding, UI declarations, and chat-mode external tool lists.

The MVP implementation uses its own worktree, rebased onto main after Vault's integration. Reuse `ProductWorkspaceShell`, `WorkspaceToolbarFrame`, `ChatArea`, profile conversations, split controls, authentication, and the shared product navigation. Define Knowledge Base in YAML and keep its content storage separate from the chat workspace.

The YAML declaration alone does not mount a React surface, register Go tool factories, or add a product to the external catalog. Those integrations are explicit implementation work. The current external catalog reads AgentWorks and Relay manifest admissions; it needs an additional Knowledge Base manifest adapter.

Implementation lives on branch `feat/knowledgebase-mvp` in the isolated `knowledgebase-mvp` worktree, rebased onto the latest main before implementation. This document is tracked under `docs/design/` in that worktree.

### 14.2 Product package and manifest

Proposed additions, relative to the selected AgentWorks repository:

```text
agent_go/
├── internal/knowledgebaseproduct/
│   ├── product.yaml
│   ├── product_config.go
│   ├── profile.go
│   ├── tools.go
│   └── prompts/access-builder.md
├── pkg/knowledgebase/
│   ├── service.go
│   ├── store.go
│   ├── permissions.go
│   ├── patch.go
│   ├── backup.go
│   └── migrations/
└── cmd/server/
    ├── knowledgebase_routes.go
    ├── knowledgebase_runtime.go
    └── external_knowledgebase.go

frontend/src/
├── products/knowledgebase/
│   ├── KnowledgebaseSurface.tsx
│   ├── KnowledgebaseWorkspacePane.tsx
│   ├── KnowledgebaseLibraryPanel.tsx
│   ├── KnowledgebaseReader.tsx
│   ├── KnowledgebaseAccessPanel.tsx
│   ├── KnowledgebaseActivityPanel.tsx
│   └── KnowledgebaseConnectPanel.tsx
└── services/knowledgebaseApi.ts
```

These are ownership boundaries, not a requirement to create every file immediately. Keep small implementations together until splitting helps maintenance.

The product manifest owns:

- Product/profile identity, version, branding, and `ui.surface: knowledgebase`.
- `profile.scope: project`, ensuring the dedicated access-builder prompt and runtime are used.
- A fixed chat workspace, `Chats/Knowledgebase`, and a singleton conversation per authenticated user and organization. These are chat artifacts, not the knowledge content storage root.
- The access-builder prompt, a narrow product tool allowlist, and direct bridge exposure of `manage_knowledgebase_access`.
- `runtime.agent_tools.mode: mcp_only`, with no general content file, shell, patch, SQL, browser, or arbitrary connected-MCP tools in this builder.
- Disabled workflow execution, raw terminal, general MCP/skill selection, and generation-secret capabilities. Reuse the shared model settings and provider configuration rather than introducing a new model picker.
- No built-in workflows or backup schedules.
- `chat.builder.tools: [manage_knowledgebase_access]` for the access chat and a distinct `chat.mcp.external_tools` list containing the content/backup names from section 7. The `mcp` declaration is catalog configuration, not a second user-facing Run chat.

Load and validate this manifest with the shared loader, register its profile and tool factory during server startup, and tag the profile with the `knowledgebase` product. Extend the shared external catalog to consume its `chat.mcp.external_tools`, validate name/schema/handler agreement, and respect product enablement.

Extend the shared runtime policy with direct `runtime.bridge_tools` support and use it for the access tool. The underlying provider bridge already supports additional registered tool names. Do not add a shell tool to the access builder. Verify the registered bridge invocation and restrict provider choices to adapters that preserve MCP-only operation without ambient personal MCP configurations.

Provider transport and tool admission must be verified together: the prompt and `ui.files_panel: false` are not authorization boundaries. Use the existing supported MCP-only provider setup, prevent ambient personal MCP configurations from being added to this builder, and expose no live content files or grant-store access in its chat workspace. If a provider cannot maintain the restricted surface, exclude it from this profile until the shared adapter is corrected.

### 14.3 Shared UI surface

Register `knowledgebase` in the shared product surface IDs/labels, deployment enablement, allowed-product checks, preloader, product switcher, and `App.tsx` rendering path. Add its icon to the existing navigation pattern; do not build a separate application header or login flow.

Layout:

```text
Shared product navigation and workspace toolbar
┌────────────────────────┬──────────────────────────────────┐
│ Access-management chat │ Library / Access / Activity /    │
│                        │ Connect                          │
│ Existing ChatArea      │ Folder tree + Markdown reader    │
│ and conversation tabs  │ or the selected management view │
└────────────────────────┴──────────────────────────────────┘
```

Reuse `ProductWorkspaceShell`, `WorkspaceToolbarFrame`, shared toolbar buttons, split controls, mobile/tablet/laptop layout behavior, `ProductChatLandingCard`, and the server-owned profile conversation APIs. Bind the selected folder as a validated context hint for chat; it never supplies an authorization identity.

- **Library:** nested folders, search/type/tag filters, read-only content, attribution, and per-entry backup status. No editor or generic writable file panel.
- **Access:** inspect effective access; Owners/admins can use chat to grant, change, or revoke folder grants. Show inherited grants as inherited rather than implying a child can cancel them.
- **Activity:** permission-filtered content/access/backup events.
- **Connect:** the actual MCP endpoint and connection instructions; reuse secure token creation/revocation controls for users and administrator-managed service accounts. Do not put credential values in chat or ordinary activity messages.
- **Models:** use the existing shared model settings where the shell exposes them.

Git configuration is administrator-managed through secure platform settings/provisioning. The reader shows backup status, but commit and push are MCP operations rather than application buttons or access-chat actions.

Unlike Vault's current administrator-only management surface, Knowledge Base must permit authorized Readers and Editors to use its viewer. Coarse product access controls whether the product can be opened; folder grants control which content and access actions are available. Enabling the product grants no automatic read or write access to its root.

### 14.4 Domain service and persistence

For MVP, access control uses a dedicated server-owned SQLite database on persistent storage, with migrations, transactions, foreign keys, and WAL mode. It holds folder grants only. Run the backend on one host; any worker processes share that host's data volume. Do not assume SQLite on a network filesystem supports horizontal scaling. The app's browser and agents never open it directly. Reuse platform database/secret infrastructure where applicable, but do not expose generic SQL tools to the product builder.

Use a separate process-safe publication lock with an OS advisory lock (flock), durable private receipt files, and private Git refs that retain prepared commit objects. Receipt state and ownership remain outside the exported branch. Acquire the publication lock non-blockingly and bound the Git subprocess duration. A busy or failed backup therefore does not hold up ordinary live writes. Restart recovery reconciles any durable unknown receipt before the next explicit publication.

Store the section 10 records in their specified homes (sqlite for grants; files, Git, and existing platform stores for the rest), with every domain key partitioned by organization. Resolve the organization from trusted installation/account configuration and authenticated execution context; never let a tool argument select an unauthorized organization. Bind platform user IDs to domain identities, and add explicit service-account execution principals where needed.

One service performs authorization and business operations for all callers. App reader endpoints, builder access actions, first-party runtime tools, and external MCP handlers call it with server-resolved principals. Folder ancestor evaluation and connection caps are central helpers, not independently implemented per transport.

Store live Markdown as files. Use content-hash plus registry-sequence version comparison for writes, and generate backup commits only in the backend's private staging repo. Adapt the reusable patch logic to operate on a temporary content copy; the unrestricted workspace HTTP handler must not become a bypass around domain permissions.

Start with permission-filtered literal keyword search over the live files, reusing the existing ripgrep-backed search handler, with pagination; filter every page by folder grant before returning. A public cursor is opaque and bound to its query/identity scope, and authorization is re-evaluated on each page. No semantic search or graph storage is required.

### 14.5 Builder access tool

Register one product tool, `manage_knowledgebase_access`, with explicit operations for listing eligible identities/folders, inspecting direct/effective grants, granting a role, changing a direct grant, and revoking a direct grant.

The tool factory binds the actual `ToolRuntimeContext` identity, organization, and owned interactive profile conversation. Inspect returns an opaque folder ACL version; grant mutations require that expected ACL version and a request ID. Atomically check the target folder's current Owner/admin authority and ACL version before changing its direct grants, then advance the ACL version. Parent grants are still evaluated dynamically. An inherited grant can only be changed at its granting ancestor; a child revoke must not fabricate an explicit deny.

Do not accept actor/admin flags from model arguments, expose credential material, or copy Vault's privileged upstream setup authority. The access-builder bridge principal can call access actions only; it cannot use content mutation or backup routes, even when the underlying user is an Editor. Ordinary content MCP credentials cannot invoke this builder's grant-management tool.

Successful access changes emit the existing product/chat event pattern so the Access view refreshes. Content changes originating in other clients invalidate the reader/search cache and refresh the visible viewer through shared events or bounded version polling.

### 14.6 First-party and external MCP access

Add the tools and validated schemas from section 7 to the shared external catalog/dispatch and the knowledge-base runtime adapter. Add explicit connection capabilities `knowledgebase:read` and `knowledgebase:write`; write includes read. These capabilities admit operation families, while live folder grants and stored folder scopes still authorize each target.

The existing hosted endpoint is `/api/external/v1/mcp`. It advertises `get_api_spec` and `call_tool`; actual product operation names are discovered and invoked through that catalog. Reuse that transport for the MVP:

```text
get_api_spec(names="update_knowledgebase")
call_tool(name="update_knowledgebase", arguments={...})
```

Thus `update_knowledgebase` is the public knowledge operation name even when the hosted MCP transport uses a generic call wrapper. Existing local bridge configurations can expose registered named operations directly where supported. Do not assume adding a manifest creates a separate `/knowledgebase/mcp` endpoint or automatically renames an unrelated registered tool.

Extend the shared token issuance, scope filtering, discovery instructions, and dispatch to support Knowledge Base and its folder restrictions. Dedicated service-account tokens must resolve to a service-account principal rather than an impersonated administrator; this needs explicit implementation if the current platform token path supports users only.

Crews, Code, and Workflows connect using their existing MCP integration selection and credential storage. They can use a delegated user connection or a scoped service-account connection. Project selection is a convenience and never grants folder access. Preserve caller identity at runtime, and recheck live permissions rather than sharing a cached administrator credential among products.

### 14.7 Implementation sequence and verification

1. **Domain foundation:** grant-store schema/migrations, identities, nested folders, names, inheritance, connection caps, and the centralized authorizer. Prove organization and folder isolation first.
2. **Content operations:** creation, keyword search, bounded selectors, metadata edits, deletion tombstones, idempotency, and version-checked patch persistence using the reused engine.
3. **Product registration and viewer:** manifest loading, startup registration, product entitlement/navigation, shared shell, and reader endpoints. Verify Readers can use it without administrative privileges.
4. **Access builder:** direct bridge tool, purpose-bound execution principal, restricted provider surface, grant version checks, shared conversations, and Access view refresh.
5. **MCP integrations:** catalog admission, token scopes, first-party adapter, hosted discovery/calls, and local Claude Code read/update through the actual endpoint.
6. **Git adapter:** isolated commits, authorized deletion selection, publication lock, receipt lifecycle, branch conflict behavior, unknown-outcome recovery, and accurate backup status.
7. **Integrated verification:** exercise the section 12 cases across app and MCP; restore a grant-store/registry/content backup in a controlled environment before considering recovery verified.

Use meaningful tests for authorization, concurrent writes, atomic patch failure, and Git publication/recovery. Reuse the existing product-surface contract tests to check manifest declarations against registered tools and prove the builder cannot discover or execute content/backup/general-purpose tools. Run UI checks for the shared desktop/mobile layouts and a live provider bridge call; a parsed YAML file alone does not verify the product.

### 14.8 Inspected implementation references

These references identify the existing mechanisms inspected for this plan; the current implementation checkout must be selected and verified before coding:

| Existing file, relative to the inspected `gateway-review-fixes` checkout | Reuse or extend |
| --- | --- |
| `agent_go/pkg/agentprofiles/manifest.go` | YAML schema, embedded loading, chat-mode external admission fields. |
| `agent_go/pkg/agentprofiles/types.go` | Runtime policies, direct bridge tools, workspace/conversation contracts. |
| `agent_go/internal/dominionproduct/product_config.go` | Manifest-backed product loading pattern. |
| `agent_go/internal/caplayerproduct/profile.go` | Vault profile registration and purpose-bound tool execution pattern. |
| `agent_go/cmd/server/product_tool_gate.go` | Shared product tool admission. |
| `agent_go/cmd/server/external_tools.go` | Extend manifest admission, schemas, and native external dispatch. |
| `agent_go/cmd/server/external_mcp.go` | Hosted MCP transport and discovery instructions. |
| `frontend/src/products/mcp-gateway/GatewaySurface.tsx` | Shared surface, chat, split workspace, and conversation lifecycle. |
| `frontend/src/products/productSurfaceConfig.ts` | Product IDs, labels, deployment availability, and user entitlements. |

The MVP remains a shared, permissioned knowledge base with a read-only application, access-management chat, MCP content tools, and explicit Git backups, delivered through the existing product platform.
