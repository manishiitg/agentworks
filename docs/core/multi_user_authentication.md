# Multi-User Authentication & Workspace Isolation

This document describes the multi-provider authentication system and per-user workspace isolation feature.

## Table of Contents

1. [Overview](#overview)
2. [Authentication Modes](#authentication-modes)
3. [Authentication Providers](#authentication-providers)
4. [Per-User Workspace Isolation](#per-user-workspace-isolation)
5. [Configuration](#configuration)
6. [API Reference](#api-reference)
7. [Architecture](#architecture)

---

## Overview

Runloop supports two modes of operation:

| Mode | Description | Use Case |
|------|-------------|----------|
| **Single-User** | No authentication required, uses default user ID | Local development, personal use |
| **Multi-User** | JWT authentication with multiple provider support | Team deployment, production |

In both modes, workspace files are organized per-user to ensure data isolation.

---

## Authentication Modes

### Single-User Mode (Default)

When `MULTI_USER_MODE` is not set or set to `false`:

- No login required
- All requests use a default user ID (`DEFAULT_USER_ID` env or `"default-user"`)
- Per-user folders are stored under `/_users/default-user/`
- Suitable for local development and personal deployments

### Multi-User Mode

When `MULTI_USER_MODE=true`:

- JWT authentication required for all API requests
- Multiple authentication providers supported
- Each user gets isolated workspace folders
- Per-user folders stored under `/_users/{userID}/`

---

## Authentication Providers

The system supports multiple authentication providers that can be enabled simultaneously.

### Available Providers

| Provider | Type | Description |
|----------|------|-------------|
| `simple` | Credentials | Username/password from the user directory / environment variable |
| `cognito` | OAuth | AWS Cognito User Pool with hosted UI |
| `supabase` | Credentials | Email/password verified against Supabase Auth |
| `supabase-google` | OAuth | Sign in with Google, hosted by Supabase Auth (PKCE) |

### Simple Provider

Username/password authentication against the **user directory**,
`config/users.json` in the shared workspace (argon2id hashes, never plain
text). See `docs/design/user_accounts_and_workflow_sharing.md` for the model.

**Bootstrap** — the directory is seeded from the environment on the first
start and the env vars can then be removed:
```bash
AUTH_USERS=admin:password123,user1:secret456   # imported (hashed) into config/users.json once
ADMIN_USERS=admin                              # usernames or emails that are admins
```

After that, accounts are managed in the app: an admin opens **Users & access**
(the shield button in the workflow toolbar, multi-user mode) or calls the admin
API below. `AUTH_USERS` keeps working as a login fallback for any name not yet
in the directory, so nothing breaks mid-migration.

**Account record** (`config/users.json`):
```json
{ "users": [ { "id": "…", "username": "carol", "email": "", "password_hash": "$argon2id$…",
               "admin": false, "can_create": false, "products": ["video-studio"], "disabled": false } ] }
```
- `admin`: manages users and product access; can open any workflow.
- `can_create`: `false` is the **read-only user** — cannot create anything, sees only what is shared.
- `products`: which product surfaces the account may open. Admins ignore it; a member with an empty
  list may open all; a read-only account with an empty list may open none.
- SSO users (Cognito/Supabase) are created on first login with nothing enabled unless `ADMIN_USERS`
  names them; an admin switches them on.
- A disabled account is refused immediately, even with a still-valid token.

### Workflow Permissions

Workflows live in the shared `Workflow/` folder. Account capabilities come from the user directory. A workflow's owners, editors and Share settings decide who can build or use that workflow. An administrator manages accounts and product access. The website and MCP apply the same checks.

Use the workflow's Share settings to give someone access. There is no separate workflow grant file or environment-based user list.

### Cognito Provider

OAuth authentication via AWS Cognito hosted UI.

**Configuration:**
```bash
AUTH_PROVIDERS=cognito
COGNITO_USER_POOL_ID=us-east-1_xxxxx
COGNITO_CLIENT_ID=xxxxxxxxx
COGNITO_DOMAIN=myapp.auth.us-east-1.amazoncognito.com
AWS_REGION=us-east-1
```

**Features:**
- Enterprise SSO support
- User pool management via AWS Console

### Supabase Provider

Supabase Auth, in two flavors that can be enabled independently:

| Provider name | Type | Login UI |
|---|---|---|
| `supabase` | Credentials | Email/password form, verified against Supabase Auth |
| `supabase-google` | OAuth | "Sign in with Google" button (Supabase-hosted Google OAuth, PKCE) |

**Email/password configuration:**
```bash
AUTH_PROVIDERS=supabase
SUPABASE_URL=https://xxx.supabase.co
SUPABASE_ANON_KEY=eyJxxx
```

**Google sign-in setup** (once per deployment):

1. Supabase dashboard → create a project → copy its Project URL and anon public key.
2. Supabase dashboard → Authentication → Providers → enable Google. It asks for a Google OAuth client:
   - Google Cloud Console → APIs & Services → Credentials → Create OAuth client ID (Web application).
   - Authorized redirect URI: `https://<your-project-ref>.supabase.co/auth/v1/callback` (Supabase's callback, not the app's).
   - Paste the client ID/secret back into Supabase's Google provider settings.
3. Supabase dashboard → Authentication → URL Configuration → Redirect URLs: add `https://<your-app-host>/auth/callback` (plus `http://localhost:<port>/auth/callback` for local dev). Set Site URL to the app origin.
4. Server env:
```bash
MULTI_USER_MODE=true
AUTH_PROVIDERS=supabase-google   # add simple and/or supabase to keep password options
SUPABASE_URL=https://xxx.supabase.co
SUPABASE_ANON_KEY=eyJxxx
AUTH_SECRET=<long random value>
ADMIN_USERS=you@gmail.com        # your first Google login becomes admin
AUTH_ALLOWED_EMAILS=you@gmail.com,teammate@example.com  # optional exact OAuth allowlist
```

When `AUTH_ALLOWED_EMAILS` is set, OAuth sign-in accepts either an exact email from that legacy allowlist or an account that an administrator has already provisioned in `config/users.json`. The user directory is the durable source of truth for invitations, so adding a user in **Users & access** never requires a second environment-variable edit or service restart. Providers that return no email are rejected. A pre-provisioned account with a matching normalized email keeps its existing AgentWorks user ID, projects, history, and permissions; its first successful Google login links the SSO identity to that account. A disabled directory account remains rejected. Otherwise, the first Google login auto-creates an account with nothing enabled unless `ADMIN_USERS` names the email, and an admin then sets role/products in Users & access. Unlike Cognito, Supabase does not echo our CSRF state back — the callback falls back to its session-stored state, and the server-side PKCE verifier still binds the code to the flow that started it.

**Deterministic invite and first-login contract:**

1. An administrator creates the account in **Users & access** with the exact email the person will use with SSO and assigns products and account permissions there.
2. That persisted directory record is sufficient admission approval. Deployment scripts do not edit `AUTH_ALLOWED_EMAILS` for individual invitations.
3. On first SSO login, the callback matches the verified provider email case-insensitively, links the provider and external ID to the existing record, and keeps the record's stable AgentWorks user ID.
4. Every later request reloads account and workflow permissions from the directory and workflow manifests. SSO linkage does not grant additional products or workflow access.
5. Disabling the directory record blocks login and authenticated requests. Removing a person only from `AUTH_ALLOWED_EMAILS` does not revoke an explicitly provisioned account; disable the account in **Users & access** instead.

Regression tests must prove all three admission branches: a provisioned email succeeds even when absent from `AUTH_ALLOWED_EMAILS`, a legacy allowlisted email succeeds, and an unknown email is denied. The existing SSO-link test also proves that first login retains the pre-provisioned user ID.

**Features:**
- Social login via Supabase-hosted Google OAuth
- Email/password authentication
- Row-level security integration

### Multiple Providers

Enable multiple providers simultaneously:

```bash
AUTH_PROVIDERS=simple,cognito,supabase
AUTH_USERS=admin:password123
COGNITO_USER_POOL_ID=us-east-1_xxxxx
COGNITO_CLIENT_ID=xxxxxxxxx
COGNITO_DOMAIN=myapp.auth.us-east-1.amazoncognito.com
SUPABASE_URL=https://xxx.supabase.co
SUPABASE_ANON_KEY=eyJxxx
```

The login page will display all configured providers.

---

## Per-User Workspace Isolation

### Folder Structure

The workspace uses a hybrid folder model:

```
/app/workspace-docs/
├── _users/                    # Per-user isolated folders
│   ├── default/               # Fallback for single-user mode
│   │   ├── Chats/             # User's chat history
│   │   ├── Downloads/         # User's downloads
│   │   └── (plan folders live under Chats/)
│   └── user-abc123/           # Multi-user: each user gets own folder
│       ├── Chats/
│       └── Downloads/
├── Chats -> _users/default/Chats   # Symlink (for shell command access)
├── Downloads -> _users/default/Downloads
├── skills/                    # Shared across all users
└── Workflow/                  # Shared across all users
```

### Folder Classification

| Folder | Type | Description |
|--------|------|-------------|
| `Chats/` | **Per-User** | Chat session outputs, skill files, user scripts, multi-agent plan folders |
| `Downloads/` | **Per-User** | User downloads and imports |
| `skills/` | **Shared** | Installed skills/templates |
| `Workflow/` | **Shared** | Workflow definitions and runs |

### How It Works

1. **User ID Resolution:**
   - Multi-user mode: User ID from JWT token claims
   - Single-user mode: Default user ID from environment (`"default"`)

2. **Path Routing (Document/File API):**
   - Requests to `Chats/*` or `Downloads/*` → `/_users/{userID}/...`
   - Requests to `skills/*`, `Workflow/*` → root level (shared)
   - Implemented in `workspace/utils/path.go` via `ResolveUserPath()`

3. **Symlinks for Shell Commands:**
   - On startup, `EnsurePerUserSymlinks()` creates root-level symlinks: `Chats/ -> _users/{userID}/Chats/`
   - Shell commands can use logical paths (e.g., `cat Chats/file.md`) and the symlink resolves to the physical per-user location
   - Symlinks are per the default user in single-user mode; multi-user deployments use the Isolator's WritePathMappings instead

4. **Automatic Migration:**
   - On startup, existing `Chats/` and `Downloads/` at root level are migrated to `/_users/default/`
   - One-time migration for backwards compatibility

### User ID Flow

```
┌─────────────────┐     ┌─────────────────┐     ┌─────────────────┐
│   AuthMiddleware│ ──► │  Agent Context  │ ──► │ Workspace Client│
│   (extracts ID) │     │  (user_id key)  │     │ (X-User-ID hdr) │
└─────────────────┘     └─────────────────┘     └─────────────────┘
                                                        │
                                                        ▼
                                                ┌─────────────────┐
                                                │  Workspace API  │
                                                │ (resolves path) │
                                                └─────────────────┘
```

### Shell Command Isolation (FolderGuard)

Shell commands (`execute_shell_command`) run inside the workspace Docker container and are sandboxed using Linux mount namespaces via `unshare -m`. The FolderGuard system controls what the LLM can read and write.

#### FolderGuard Modes

| Mode | When Used | Mechanism |
|------|-----------|-----------|
| **Deny-list** (Mode 1) | Chat mode (default tools) | Hides `_users/` with tmpfs overlay; everything else visible |
| **Allow-list** (Mode 2) | Multi-agent / workflow mode | Hides entire workspace with tmpfs, then selectively bind-mounts ReadPaths (read-only) and WritePaths (read-write) |

#### Mode 1: Deny-List (Chat Mode)

The default FolderGuard (`getDefaultFolderGuard()`) blocks only `_users/` to prevent direct access to the internal per-user directory structure. The LLM accesses per-user folders via their logical symlinked paths (e.g., `Chats/`).

```
BlockedPaths: ["_users"]     # Hidden with tmpfs
ReadPaths:    []             # Not used (everything else is visible)
WritePaths:   []             # Not used
```

The agent backend additionally restricts which folders the LLM can **write** to via `wrapExecutorsWithChatModeFolderGuard()` — writes are only allowed to `Chats/` (and `skills/custom/` if the skill creator is active). This is enforced at the agent level before the shell command reaches the workspace API.

#### Mode 2: Allow-List (Multi-Agent Chat)

Multi-agent chat sub-agents use `wrapExecutorsWithChatModeFolderGuard()` with the standard Chats/ allow list:

```
ReadPaths:  ["Chats/", "Downloads/", "skills/", "subagents/", "Workflow/", "config/", "memories/"]
WritePaths: ["Chats/", "Downloads/", "config/", "memories/"]
```

The Isolator creates a mount namespace:
1. Bind-mounts the original workspace to a temp location
2. Covers `/app/workspace-docs` with tmpfs (hides everything)
3. Bind-mounts ReadPaths back (read-only) from the temp copy
4. Bind-mounts WritePaths back (read-write) from the temp copy

#### Per-User Write Path Mappings

For per-user folders (Chats/), the shell handler creates `WritePathMappings` that map logical paths to physical per-user paths:

```
WritePaths:        ["Chats/"]
WritePathMappings: {
  "Chats/": "_users/default/Chats/"
}
```

The Isolator uses these mappings to source files from `_users/{userID}/Chats/` while mounting them at the logical `Chats/` path. This way, shell commands use logical paths transparently, and each user's data stays isolated.

#### Protected Folder Enforcement

The agent backend enforces additional restrictions before shell commands reach the workspace API:

- `_users/` folder references in shell commands are **blocked** (prevents bypassing isolation)
- `Workflow/` folder references are **blocked** in chat mode (workflows have their own mode)
- Write operations to folders outside the allowed list are **rejected** with an error message

---

## Configuration

### Environment Variables

#### Core Settings

| Variable | Default | Description |
|----------|---------|-------------|
| `MULTI_USER_MODE` | `false` | Enable multi-user authentication |
| `AUTH_SECRET` | dev default | JWT signing secret (required in production) |
| `DEFAULT_USER_ID` | `default-user` | Default user ID for single-user mode |

#### Simple Provider

| Variable | Required | Description |
|----------|----------|-------------|
| `AUTH_USERS` | Yes | Comma-separated `user:pass` pairs |

#### Cognito Provider

| Variable | Required | Description |
|----------|----------|-------------|
| `COGNITO_USER_POOL_ID` | Yes | AWS Cognito User Pool ID |
| `COGNITO_CLIENT_ID` | Yes | Cognito App Client ID |
| `COGNITO_DOMAIN` | Yes | Cognito hosted UI domain |
| `AWS_REGION` | Yes | AWS region (e.g., `us-east-1`) |

#### Supabase Provider

| Variable | Required | Description |
|----------|----------|-------------|
| `SUPABASE_URL` | Yes | Supabase project URL |
| `SUPABASE_ANON_KEY` | Yes | Supabase anonymous key |

### Example Configurations

#### Development (Single-User)

```bash
# No authentication required
MULTI_USER_MODE=false
```

#### Development with Simple Auth

```bash
MULTI_USER_MODE=true
AUTH_PROVIDERS=simple
AUTH_USERS=admin:admin123
AUTH_SECRET=dev-secret-change-me
```

#### Production with Cognito

```bash
MULTI_USER_MODE=true
AUTH_PROVIDERS=cognito
AUTH_SECRET=your-production-secret
COGNITO_USER_POOL_ID=us-east-1_xxxxx
COGNITO_CLIENT_ID=xxxxxxxxx
COGNITO_DOMAIN=myapp.auth.us-east-1.amazoncognito.com
AWS_REGION=us-east-1
```

---

## API Reference

### Authentication Endpoints

#### Get Auth Mode

```http
GET /api/auth/mode
```

**Response:**
```json
{
  "multi_user_mode": true,
  "providers": [
    {"name": "simple", "type": "credentials"},
    {"name": "cognito", "type": "oauth"}
  ]
}
```

#### Login (Credentials)

```http
POST /api/auth/login
Content-Type: application/json

{
  "username": "admin",
  "password": "password123",
  "provider": "simple"
}
```

**Response:**
```json
{
  "token": "eyJhbGc...",
  "user": {
    "user_id": "abc123",
    "username": "admin",
    "provider": "simple"
  }
}
```

#### Start OAuth Flow

```http
GET /api/auth/start?provider=cognito
```

**Response:** Redirects to OAuth provider

#### OAuth Callback

```http
GET /api/auth/callback?provider=cognito&code=xxx&state=xxx
```

**Response:** Exchanges code for app JWT and redirects to frontend

### Account management endpoints (admins only)

| Method | Path | Purpose |
|---|---|---|
| GET | `/api/admin/users` | List accounts (never hashes) plus the product ids this server can host |
| POST | `/api/admin/users` | Create: `username`, optional `email`, `password` (≥8, blank = SSO only), `admin`, `can_create`, `products` |
| PUT | `/api/admin/users/{id}` | Update any of the above, `password` resets it, `disabled` switches the account off. Admins cannot demote or disable themselves |
| DELETE | `/api/admin/users/{id}` | Remove the record; the user's `_users/<id>` files are kept |
| POST | `/api/auth/password` | Any user: `current_password`, `new_password` |

`GET /api/auth/me` now also returns `is_admin` and `can_create`; `allowed_products` is `null` for
unrestricted accounts, an array otherwise (an empty array means no products).

### Workflow sharing endpoints

| Method | Path | Purpose |
|---|---|---|
| GET | `/api/workflow/access?workspace_path=Workflow/<folder>` | Owners, readers, and the caller's own level (`my_access`); `legacy` when nothing is recorded yet |
| PUT | `/api/workflow/access` | `{workspace_path, owners:[…], readers:[…]}` — ids, usernames or emails; owners/admins only; at least one owner must remain |
| GET | `/api/users/directory` | id, username, email of every enabled account, for the share picker |

Each entry in `GET /api/workflows/manifests` carries `my_access`; workflows the caller may not see
are omitted. Owners may edit, share and delete; readers get exactly the PLAT-262 read-only session
(chat, run, watch, inspect) and may trigger or stop schedules but not change them.

### Workspace API Headers

The workspace API uses the `X-User-ID` header for per-user folder routing:

```http
GET /api/documents?folder=Chats
X-User-ID: user-abc123
```

This header is automatically set by the agent API based on the authenticated user.

---

## Architecture

### Authentication Flow

```
┌──────────┐                                    ┌──────────────┐
│ Frontend │  1. GET /api/auth/mode             │   Backend    │
│          │◄──────────────────────────────────►│              │
│          │  2. Show provider buttons          │              │
│          │                                    │              │
│          │  3a. POST /api/auth/login (simple) │              │
│          │──────────────────────────────────►│              │
│          │◄──────────────────────────────────│              │
│          │  4a. JWT token                     │              │
│          │                                    │              │
│          │  3b. GET /api/auth/start (OAuth)   │              │
│          │──────────────────────────────────►│              │
│          │  4b. Redirect to OAuth provider    │              │
│          │                                    │              │
│          │  5. OAuth callback with code       │              │
│          │◄──────────────────────────────────│              │
│          │  6. App JWT token                  │              │
└──────────┘                                    └──────────────┘
```

### Key Files

#### Backend

| File | Description |
|------|-------------|
| `agent_go/cmd/server/auth_middleware.go` | JWT validation, user context |
| `agent_go/cmd/server/auth_providers.go` | Provider interface, implementations |
| `agent_go/cmd/server/user_auth_routes.go` | Login, OAuth routes |
| `agent_go/pkg/workspace/client.go` | Workspace client with user ID |
| `agent_go/pkg/common/types.go` | Context keys including `UserIDKey` |

#### Workspace API

| File | Description |
|------|-------------|
| `workspace/utils/path.go` | Per-user path resolution, symlink setup, migration |
| `workspace/handlers/documents.go` | Document handlers with user routing |
| `workspace/handlers/shell.go` | Shell command handler with FolderGuard/Isolator integration |
| `workspace/security/isolator.go` | Mount namespace isolation (unshare) with read/write path control |
| `workspace/models/shell.go` | FolderGuardConfig struct definition |
| `workspace/server.go` | Startup migration, symlink creation |

#### Frontend

| File | Description |
|------|-------------|
| `frontend/src/stores/useAuthStore.ts` | Auth state management |
| `frontend/src/pages/Login.tsx` | Login page with providers |
| `frontend/src/pages/AuthCallback.tsx` | OAuth callback handler |

---

## Security Considerations

### JWT Tokens

- Tokens expire after 24 hours
- Signed with HMAC-SHA256
- Contains: user_id, username, email, provider

### Password Storage

- Passwords are stored as argon2id hashes in `config/users.json` (64MB, 3 passes, 2 lanes)
- `AUTH_USERS` is plain text in the environment and is only a bootstrap: its users are imported
  (hashed) on first start, after which the variable should be removed
- Users change their own password via `POST /api/auth/password`; admins reset via the admin API

### User ID Validation

- User IDs are sanitized (alphanumeric, hyphens, underscores only)
- Maximum length: 128 characters
- Invalid IDs fall back to `"default"`

### Path Security

- All paths validated against directory traversal attacks
- Per-user folders isolated under `/_users/{userID}/`
- Users cannot access other users' files through API

---

## Migration

### From Single-User to Multi-User

1. Set `MULTI_USER_MODE=true`
2. Configure at least one auth provider
3. Existing files in `Chats/` and `Downloads/` will be migrated to `/_users/default/`
4. Existing users can continue with the same data after migration

### From Legacy Workspace

On first startup with this feature:

1. Server checks for existing `Chats/` and `Downloads/` at root level
2. If found with content, moves them to `/_users/default/`
3. Creates per-user folder structure
4. Shared folders remain unchanged

No manual intervention required - migration is automatic and one-time.

---

## Testing

The multi-user isolation system has comprehensive test coverage across three test files.

### Test Files

| File | Tests | Scope |
|------|-------|-------|
| `workspace/utils/path_test.go` | 38 | Path routing, user isolation, symlinks, migration |
| `workspace/handlers/documents_test.go` | 8 | Document listing API, cross-user isolation |
| `workspace/security/isolator_test.go` | 6 (multi-user) | FolderGuard mount scripts, sandbox profiles |

### Path Utilities (`workspace/utils/path_test.go`)

Tests for the core path routing logic that enforces per-user isolation.

**User ID Validation:**
- `TestIsValidUserID` — Validates allowed characters (alphanumeric, hyphens, underscores), rejects special chars, path traversal attempts (`../etc`), and enforces max length (128 chars)
- `TestSanitizeUserID` — Empty/invalid user IDs fall back to `"default"`

**Path Routing:**
- `TestIsPerUserPath` — Correctly classifies `Chats/` and `Downloads/` as per-user and `skills/`, `Workflow/` as shared
- `TestResolveUserPath` — Per-user paths routed to `_users/{userID}/`, shared paths pass through unchanged, `_users/` direct access blocked, invalid/empty user IDs fall back to default, full internal paths sanitized
- `TestConvertToUserRelativePath` — Strips `_users/{userID}/` prefix for API responses
- `TestSanitizeInputPath` — Handles relative paths, full-path stripping, `..` cleaning

**Cross-User Security:**
- `TestCrossUserIsolation` — User1 cannot access User2's files; `_users/user2/Chats` path is blocked for User1; shared folders resolve identically for all users

**Symlink Management:**
- `TestEnsurePerUserSymlinks` — Creates symlinks (`Chats -> _users/default/Chats`), idempotent on re-run, fixes wrong symlink targets, replaces empty directories with symlinks, skips non-empty directories to prevent data loss

**Migration:**
- `TestMigratePerUserFolders` — Migrates root-level `Chats/` to `_users/default/Chats/`, skips already-migrated (symlinked) folders, skips empty folders, merges content in partial migration scenarios (root + user dirs both have files)

### Document Handler (`workspace/handlers/documents_test.go`)

HTTP-level tests using `httptest` and a real Gin router to verify the document listing API.

**Root Listing Security:**
- `TestRootListingFiltersUsersDirectory` — `_users/` directory never appears in root listing; per-user folders (`Chats/`, `Downloads/`) are injected from the user's isolated directory
- `TestRootListingWithDotFolder` — `folder=.` parameter treated as root listing (same `_users/` filtering applies)

**Per-User Isolation:**
- `TestPerUserFolderIsolation` — Default user sees `session1.json` in their `Chats/` but not User2's `user2-secret.json`; User2 sees their own files but not the default user's
- `TestNoUserIDFallsToDefault` — Missing `X-User-ID` header falls back to `"default"` user

**Cross-User Access Prevention:**
- `TestDirectUsersAccessBlocked` — `folder=_users` request returns error
- `TestCrossUserAccessViaUsersPath` — `folder=_users/default/Chats` blocked for other users (prevents path-based cross-user data access)

**Shared Folders:**
- `TestSharedFoldersSameForAllUsers` — `skills/` returns identical content regardless of `X-User-ID`

### FolderGuard / Isolator (`workspace/security/isolator_test.go`)

Tests for the Linux mount namespace and macOS sandbox-exec isolation scripts.

**Deny-List Mode (Mode 1) Symlink Fixup:**
- `TestDenyListSymlinkFixup` — When `_users/` is hidden with tmpfs, symlinks like `Chats -> _users/default/Chats` would break. Verifies that the Linux mount script preserves the workspace via bind-mount and re-mounts symlink targets after tmpfs. Verifies the macOS sandbox profile adds explicit allow rules for symlink targets within the denied path.
- `TestDenyListNoSymlinks` — When no symlinks point into blocked paths, no unnecessary workspace preservation occurs (simpler script)
- `TestDenyListWithMultiUser` — With multiple users (`default`, `alice`, `bob`), only the current user's symlink targets are exposed in the mount script. Alice and bob's directories remain hidden.

**Environment Isolation:**
- `TestEnvironmentIsolation` — Secrets (`DATABASE_URL`, `API_KEY`) set in the parent process are NOT leaked to subprocess environment; safe PATH is present

### Running Tests

```bash
# All multi-user isolation tests
go test ./utils/ ./handlers/ ./security/ -v

# Path routing tests only
go test ./utils/ -v

# Document API tests only
go test ./handlers/ -run "TestRootListing|TestPerUser|TestShared|TestDirectUsers|TestCrossUser|TestNoUser" -v

# FolderGuard/Isolator tests only
go test ./security/ -run "TestDenyList" -v
```

All commands should be run from the `workspace/` directory.
