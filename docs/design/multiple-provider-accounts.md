# Multiple provider accounts for workflows and Crew

Status: core account selection is implemented. The Providers/product setup boundary
is fixed on main as of 2026-10-03; rollout and live qualification status are
tracked in [PLAT-386](../bugs/pulse_platform/app/models/plat-386.md). Historical
proposal and baseline sections below describe the design's evolution; they are
not claims that every proposed account feature has shipped.

## Problem and intended behavior

An administrator can configure a coding provider during installation through environment credentials or a server CLI login. Users should also be able to connect multiple accounts for that same provider and select one for a workflow or Crew project.

For example, the server has Cursor installed once. It offers an administrator-managed Company account, Alice's Personal account, Alice's Client account, and Bob's Personal account. Alice can select her Client account for one workflow and the Company account for another. Bob cannot see or use Alice's private connections.

The installation, provider, account connection, model, and conversation are separate concepts:

| Concept | Example | Responsibility |
| --- | --- | --- |
| Installation | Shared `cursor-agent` binary | Administrator installs and updates software |
| Provider | `cursor-cli` | Defines adapter, models, supported authentication |
| Connection | `conn_alice_client` | Selects account identity and credentials |
| Model | A model exposed by Cursor | Selects the model within an account's available catalog |
| Conversation | Native CLI session ID | Retains a particular chat's history |

Adding an account does not reinstall a provider. Account credentials never become part of workflow files.

## Current setup and selection boundary

Providers owns coding-agent installation/configuration and account creation,
login, API keys/tokens, reauthentication and removal. This is the same boundary
locally and on a server; installation availability is separate from account
readiness and permission to use that account. The installation's credentials are
labeled **Admin-managed account**. Users select only accounts available to them.

Workflow, Crew, Code and Relay consume configured accounts and choose models and
supported reasoning effort. Their Models panels do not collect credentials or
embed provider setup. The short provider list has no search field; refresh remains
available. Management links open Providers. A coding provider is
selectable only when its runtime is installed and an authorized account is usable.
A previously saved unavailable selection stays visible for diagnosis; opening the
panel must not silently switch provider or account. Existing stored project
credentials remain compatible until an explicit migration is defined.

Reasoning choices must be offered by both the product profile and the selected
model. Account ownership does not force the default effort. A model change keeps
the account and drops incompatible effort; Cursor Auto/Composer manage reasoning
without a separate selector, while Antigravity uses model variants. Forward any
saved supported effort through the runtime adapter, not only the UI.

Source behavior and verification are in PLAT-386. Cursor account-scoped model
filtering and native pricing qualification remain open in
[PLAT-387](../bugs/pulse_platform/app/models/plat-387.md) and
[PLAT-388](../bugs/pulse_platform/ops/cost/plat-388.md).

## Baseline implementation before account support

At the baseline, the header opened `CodingProvidersPanel`, filtered to coding agents. Guided setup launched an allowlisted CLI command through a PTY/WebSocket; Workflow and Crew reused the workflow provider picker.

Availability then combined runtime installation with environment/stored authentication at provider level. Workflow configuration saved a provider profile or explicit role models; Crew stored its project agent/model in `capabilities.llm_config.builder_llm`. The current ready-account rule is described above.

At this historical baseline, workflow credential support handled Claude OAuth tokens and Cursor API keys; it was groundwork for account support. Those product-scoped input fields have since been removed in favor of Providers account setup. The baseline RTS deployment supplied installation credentials with `LLM_CONFIG_LOCKED=true`; the later account policy is described under Core account implementation.

Important source locations:

- `frontend/src/components/providers/CodingProvidersPanel.tsx`
- `frontend/src/components/providers/GuidedProviderTerminal.tsx`
- `frontend/src/components/workflow/WorkflowLLMConfigurationPanel.tsx`
- `frontend/src/components/workflow/WorkflowCapabilitiesPanel.tsx`
- `frontend/src/products/work/WorkModelsPanel.tsx`
- `frontend/src/products/work/WorkSurface.tsx`
- `agent_go/cmd/server/provider_setup.go`
- `agent_go/cmd/server/provider_keys_store.go`
- `agent_go/cmd/server/workflow_provider_auth.go`
- `agent_go/cmd/server/multiagent_llm_tools.go`
- `agent_go/cmd/server/llm_config_handlers.go`
- Sibling `multi-llm-provider-go/pkg/adapters/` implementations.

## Temporary workspaces and persistent accounts

A process's current working directory is independent of its home and provider configuration directory. A workflow may run in a temporary workspace; Crew may use a retained project workspace. Neither location must hold the durable account login.

Example Claude launch context:

```text
Working directory: /tmp/workflow-run-123
HOME:              /data/provider-connections/conn_alice/home
CLAUDE_CONFIG_DIR: /data/provider-connections/conn_alice/claude
Native session ID: session_workflow_123
```

Claude works on files in the working directory and authenticates through the selected connection's configuration. A second process uses a different connection directory for a different account. Removing `/tmp/workflow-run-123` does not remove either account.

Use three storage lifetimes:

| Storage | Contains | Lifetime |
| --- | --- | --- |
| Connection | Durable login, refresh state, account metadata | Until disconnected/deleted |
| Conversation runtime | Native transcripts, resume data, generated MCP/settings overlays | Until conversation retention expires |
| Run workspace | Work files, temporary artifacts | Defined by workflow/project execution |

Do not assume a CLI separates credentials from settings and transcripts cleanly. Each adapter must define its storage layout. Credentials must not be copied into the workspace, committed, or removed by ordinary run cleanup.

## Connection records

Introduce a server-side connection entity:

```json
{
  "id": "conn_alice_client",
  "provider": "cursor-cli",
  "display_name": "Client account",
  "scope": "user",
  "owner_user_id": "alice",
  "auth_method": "api_key",
  "credential_source": "encrypted_store",
  "status": "ready",
  "credential_version": 1
}
```

The public response also exposes safe account identity, timestamps, and an authentication error when appropriate. Secret references, private directories, raw credentials, and login transcripts are internal only. Suggested states: `pending`, `ready`, `needs_auth`, `unavailable`, and `disabled`.

Initially support `global` administrator connections and `user` private connections. Team/project sharing can later add explicit grants without changing workflow references.

API keys and supplied OAuth tokens belong in encrypted storage. CLI-managed login files need encrypted persistent storage, restricted filesystem access, backup protection, and provider-controlled refresh behavior. Do not parse and reconstruct undocumented credential formats as the default approach.

Paths are generated from opaque server IDs. The browser never supplies a filesystem path or arbitrary launch environment.

## Provider-specific design

### Claude Code (`claude-code`)

Supported approaches: supplied `CLAUDE_CODE_OAUTH_TOKEN`, or browser login through a connection-scoped `claude auth login`. An Anthropic API key is a different authentication/billing mode and must be labeled explicitly if enabled; it must not be silently treated as a subscription login.

For browser login, allocate persistent connection-specific `HOME` and `CLAUDE_CONFIG_DIR`, and use them for login, status, usage, run, and resume. On Linux Claude stores `.credentials.json` under its configuration directory. Current official documentation also describes directory-specific Keychain entries on macOS; validate the installed version/platform before supporting that path.

Remove ambient Claude credentials and alternative provider/gateway configuration before constructing the selected account environment. An inherited administrator API key can override a personal subscription login. Include settings-based credential helpers in this review.

Original adapter gap: `prepareClaudeUserConfig` previously resolves the backend home and writes `.claude.json`; transcript readers also resolve home-based `.claude/projects` paths. Pass resolved connection/runtime paths explicitly to these helpers. Changing only the child environment would leave settings and transcript reads pointed at the wrong account.

#### Identified Claude path bug: helpers use the backend home

The child Claude process and the backend adapter currently resolve storage separately:

- `multi-llm-provider-go/pkg/adapters/claudecode/claudecode_interactive_adapter.go`: `prepareClaudeUserConfig` calls `os.UserHomeDir()` and writes `<backend-home>/.claude.json`.
- `multi-llm-provider-go/pkg/adapters/claudecode/claudecode_transcript_path.go`: transcript lookup calls `os.UserHomeDir()` and searches `<backend-home>/.claude/projects`.

Setting `HOME` and `CLAUDE_CONFIG_DIR` only in a child process does not change the backend's environment or the paths these helpers resolve. For example, Claude launched for connection A could write native transcripts beneath connection A's configuration directory while the adapter searches the shared server directory.

Consequences for the proposed multi-account implementation include missing transcript-based responses/usage, incorrect transcript lookup, and trust/onboarding settings being written to shared server configuration instead of the selected account. This is a confirmed source-code gap; it is not evidence of a current production failure or an observed cross-account data leak. The existing shared-home setup may work because the backend and CLI resolve the same location.

Implemented fix (see implementation status below): carry the resolved account/runtime paths into settings preparation, transcript lookup, usage/message readers, and resume handling. Resolve each file according to the installed Claude version's directory contract; do not assume `.claude.json` and transcript roots follow identical rules. Never temporarily change the backend's global environment to make `os.UserHomeDir()` return another user's home.

Verification must seed a misleading shared backend directory, launch two connections with distinct paths, and prove that settings and transcript/usage lookup use only the intended connection. Cover both structured and interactive execution paths, including retained-session resume.

Start with persistent per-connection configuration, separate native session IDs, and run-specific project/MCP settings. If stronger per-conversation settings isolation is needed, verify a supported way to share authentication without unsafe credential copies or refresh races.

### Codex CLI (`codex-cli`)

Existing code supports `CODEX_API_KEY` and device browser login via `codex login --device-auth`. The adapter already contains `CODEX_HOME`-aware native transcript and profile handling.

Assign each browser-login connection a persistent Codex home. Login, status, execution, generated profiles, and resume must resolve that selected home. Review the installed CLI's credential-store mode, including OS keychain behavior, rather than assuming an `auth.json` file is always the active store.

For API-key connections, inject only the selected key through the adapter's supported authentication contract. `OPENAI_API_KEY` configures the direct OpenAI provider in this application; it is not the application's Codex credential field. Test the complete CLI launch path, not merely presence of `CODEX_API_KEY`.

Per-session profiles containing bridge credentials must remain separate even when multiple conversations use one account. Native session lookup must include connection identity so a transcript from another Codex home cannot be selected.

### Cursor CLI (`cursor-cli`)

Prefer encrypted `CURSOR_API_KEY` connections for the first release. Cursor officially supports both API keys and browser login. Every launch receives only the selected key; unset inherited `CURSOR_API_KEY` and `CURSOR_AUTH_TOKEN` first.

Use isolated home/XDG locations for private state and retained chats. Resolve the executable to its installed absolute path before replacing home, because a shared binary may be installed beneath the service user's home.

Browser login needs a version-specific storage audit: verify which home/XDG paths and credential services `cursor-agent login`, `status`, and `logout` use. A separate directory is not sufficient evidence that an OS credential service is isolated. Enable this method only after proving two accounts can sign in, run, resume, and log out independently.

Current retained-turn code includes backend-home resolution. Make it account-aware. Keep workspace `.cursor` MCP/rules/hooks configuration scoped to the run, without changing shared account settings.

### Pi CLI (`pi-cli`)

Pi is a router to multiple underlying providers. A connection should identify the underlying provider as well as the Pi adapter, for example Personal Gemini or Client Anthropic. A model selected through that connection must belong to its allowed underlying provider. Two accounts for the same underlying provider become two separate connections.

The local adapter already sets `PI_CODING_AGENT_DIR` and `PI_CODING_AGENT_SESSION_DIR` to session-specific locations under the working directory, and uses exclusive MCP configuration. Merely setting a connection directory earlier in the request would be overridden by this behavior.

Preserve exclusive per-session MCP/settings/transcripts while adding an explicit selected credential input. API-key connections inject only the intended underlying provider key. Browser/OAuth connections require an adapter-owned authentication store integration or a verified mechanism to make the selected connection's login available to the isolated runtime.

Do not copy `auth.json` independently into every run and assume refresh remains correct. If a temporary credential projection is necessary, define synchronization, atomic writes, refresh ownership, cleanup, and logout propagation before implementation. This is an implementation prerequisite for Pi OAuth accounts.

Existing shared extension-cache links must remain code caches only; they must not expose another connection's credentials or configuration.

### Muse (`muse-cli`)

Existing code supports `META_API_KEY` and `muse login`. Prefer encrypted API-key connections initially.

Muse settings currently live at `$XDG_CONFIG_HOME/muse/settings.json`, and the adapter creates isolated settings for each launch to supply MCP/policy configuration. Account selection must not remove that isolation or place all workflows' settings into one mutable account file.

Inject the selected `META_API_KEY` into the process while retaining per-session settings. Browser-login support needs an audit of Muse's actual credential store and whether changing XDG settings also changes authentication lookup. Define a persistent account home and a separate session settings directory only after verifying login/run compatibility.

Status, usage, resume, and settings helpers must receive the same resolved account context. Never fall back to the ambient Muse login when a personal connection cannot authenticate.

### Direct API providers

The header currently exposes coding agents, but the same connection model can cover direct API integrations. These connections generally do not require CLI login homes. Keep their credentials in encrypted storage and pass the selected configuration to a request-scoped client.

| Provider/family | Connection configuration |
| --- | --- |
| OpenAI | API key; optional supported organization/project/endpoint fields |
| Anthropic | API key; supported endpoint fields |
| OpenRouter | API key; supported endpoint fields |
| Vertex/Gemini | API key or explicit service-account/workload identity reference, project/location |
| Bedrock | Explicit AWS credential/role reference and region |
| Azure | API key or supported identity, endpoint, API version and supported deployment configuration |
| Z.AI, Kimi, MiniMax, DeepSeek and other Pi backends | Underlying provider identifier, credential and supported endpoint/model configuration |

This table describes connection families, not a promise that every integration is selectable as a workflow execution agent. Existing provider restrictions and supported capabilities still apply. Discover currently supported auth fields before exposing them.

Cloud SDK default credential chains can silently use the server's identity. Personal connections must use explicit request-scoped credentials and fail if unavailable. Only an explicitly labeled administrator connection may use the server identity. Cache clients by connection and credential version, never just provider name.

## Selecting accounts in workflow and Crew

Providers page: choose provider, see accessible accounts, add/name/authenticate an account, inspect status/usage, reauthenticate, or disconnect. Provider installation status remains separate from each account's status.

Product Models: choose a ready provider/account, then use provider defaults or pin models and supported reasoning effort. Account setup remains in Providers. Save immediately through the existing manifest update flow.

```json
{
  "schema_version": 2,
  "mode": "provider_profile",
  "provider": "claude-code",
  "connection_id": "conn_alice_personal"
}
```

Schema version 3 is a proposal and requires coordinated frontend/backend validation and migration. Advanced configuration places `connection_id` alongside `provider` and `model_id` on every Builder, Pulse, and execution-tier entry. In the first release, require complete role bindings rather than ambiguous implicit inheritance. Per-step model overrides must also resolve a valid matching connection.

Crew saves the connection alongside its explicit `builder_llm` provider/model. Changing connection is an account boundary even if provider and model stay the same. Create a new native conversation; keep existing chats bound to their original account, following the current provider-change behavior. The UI must explain which chats use the new selection.

Model inventories, readiness and quota can vary by account. Key discovery/status caches by connection, credential version, and relevant underlying provider. Never show another account's private inventory or identity through provider-level cache reuse.

## Backend account context and launch contract

### Worked example: a workflow uses Codex account B

Assume Codex is installed once on the server. Account A is the administrator's global connection, and account B is Alice's private connection. Both use the same binary but have separate authentication storage:

```text
conn_codex_A -> /data/provider-connections/conn_codex_A/codex
conn_codex_B -> /data/provider-connections/conn_codex_B/codex
```

1. **Alice connects B.** The server allocates B's persistent directories, then starts the guided `codex login --device-auth` process with B's `HOME` and `CODEX_HOME`. Alice completes browser authentication for account B. The selected credential-store configuration must preserve that login in B's isolated storage; verify this for the deployed CLI version. Closing the login terminal leaves the connection intact.
2. **Alice selects B in the workflow.** In Setup, she chooses Codex and account B. The workflow saves the reference below; it does not save tokens or private paths.
3. **A run resolves B.** The server reads the reference and verifies that the execution principal can use `conn_codex_B`, that it belongs to `codex-cli`, and that it is ready. Provider defaults resolve the role's model; the connection resolves its account identity.
4. **The server launches Codex with B's context.** The run workspace may be temporary. `CODEX_HOME` points to B's persistent Codex storage, independently of the workspace. The environment excludes A's credentials and conflicting credential sources. Per-conversation profile files keep MCP credentials and policy separate from other runs using B.
5. **The run retains its binding.** Persist the connection ID, native session ID, and applicable runtime paths with the conversation. Transcript/usage readers use B's resolved Codex home rather than the backend's ambient home.
6. **Resume and scheduled runs resolve B again.** Recheck authorization and readiness, then use B's storage and the original native session binding. If B needs authentication, fail with an account-specific error. Never silently run as A. If the temporary workspace was removed, restore the required workspace/context according to the existing resume contract; account persistence alone does not restore work files.

Example saved workflow configuration:

```json
{
  "schema_version": 2,
  "mode": "provider_profile",
  "provider": "codex-cli",
  "connection_id": "conn_codex_B"
}
```

Illustrative resolved launch context for one role:

```text
Executable:        /opt/agentworks/bin/codex
Working directory: /tmp/workflow-run-123
HOME:              /data/provider-connections/conn_codex_B/home
CODEX_HOME:        /data/provider-connections/conn_codex_B/codex
Model:             resolved model for the workflow role
Native session ID: session_workflow_123
Connection ID:     conn_codex_B
```

The executable and paths above are examples, not current deployment paths. The backend constructs this context internally and applies it to the specific subprocess or tmux pane. No server-global environment changes are needed.

With provider-profile mode, B is the account binding for the profile's Builder, execution tiers and Pulse work. In explicit mode, each role carries its own connection reference, so a workflow could deliberately use B for Builder and an authorized account A for another role. Delegation must propagate the selected role's connection context rather than inheriting whichever account started the parent process.

For an API-key connection B, resolve B's encrypted credential instead of a browser-login store and inject it through the adapter's validated Codex authentication path. The reference, permission checks, workspace separation, and no-fallback rule remain the same.

Switching the workflow from B to A affects new launches. A retained Codex process already bound to B must not be reused as A; start a new native conversation or perform an explicitly supported migration. Separate conversation IDs allow several workflows to use B without sharing chat history, although they share B's account limits.

Resolve a server-only account context once per execution:

```text
provider, connection ID, credential version, execution principal,
auth method, credential reference, account home/config paths,
conversation runtime paths, environment additions/removals
```

The resolver checks that the connection exists, matches the provider, is allowed for the execution principal, and is usable. Both request handlers and background jobs use this resolver.

Pass it into PTY login, structured subprocesses, interactive tmux launches, native transcript readers, usage tools, and session resume. Helpers must receive paths explicitly; changing global `os.Setenv` inside a concurrent server is prohibited.

Tmux inherits its server environment. Set/unset account variables inside each pane's launch script; do not mutate the shared tmux server environment. Ensure secrets are not exposed in command arguments or ordinary logs. Preserve required PATH, CA/proxy and runtime dependencies through an allowlisted environment policy.

Persist connection ID and credential version with retained sessions. Never reuse a session solely because provider/model match. On revocation stop new launches and cancel active executions according to the documented connection policy. Credential rotation must invalidate stale cached clients and cause retained processes to renew/relaunch when necessary.

## Authorization, sharing and schedules

A workflow edit permission does not grant permission to spend another user's account. Private connections are usable by their owner; global connections require administrator-defined access. Exclude unauthorized connections from discovery and independently enforce authorization during every launch.

For schedules, persist the owner/execution principal and selected bindings. A scheduled run uses that principal's authorized connections, not whichever user last visited the UI. If ownership/access changes, pause or fail clearly rather than selecting another account.

Recheck permissions for delegated/background work, Pulse reviews, retries, usage terminals, native resume and websocket attachment. Setup sessions are owned by a user and connection, and conflicts are keyed by connection rather than provider.

Deleting a referenced connection should be blocked with a list of affected workflows/projects/schedules. Disabling/revoking it can immediately stop new use. Reauthentication does not change the connection ID; display changed account identity and require deliberate reassignment if it is no longer the intended account.

## Filesystem and execution isolation

Private directories prevent accidental mixing, but two processes under one OS UID can still read one another's files. Run untrusted user execution under a container or equivalent filesystem/process boundary that exposes only the authorized workspace and selected account state. Do not expose the complete connection root, Docker socket, host secrets, or another account's process environment.

Use restrictive permissions, encrypted persistent volumes and protected backups. Login files must be writable where the CLI needs renewal; read-only mounts cannot be assumed to support refresh. Keep connection authentication out of workspace snapshots and downloads. Explicitly separate native session retention from credential retention.

## Migration and administrator policy

Represent current environment credentials as administrator-managed connections with `credential_source=environment`. Resolve secrets on the server rather than copying them to the browser. Represent existing global CLI logins as legacy global connections; inventory their native session dependencies before moving files.

Backfill existing workflow/project bindings to their previous connection after verifying the provider/account mapping. Do not assign a new default merely because another connection is ready. Missing or ambiguous mappings require attention. Rollout must preserve existing retained conversations and their storage paths until migration completes.

Replace or supplement `LLM_CONFIG_LOCKED` with explicit controls for allowed providers, personal connections, permitted global connections and account/model selection. Preserve locked behavior by default for existing restricted installations. RTS must deliberately enable personal connections; do not silently remove its installation policy.

## Suggested implementation stages

1. Connection storage, authorization, encrypted credentials, global environment connections, and explicit manifest/session bindings.
2. Account selection in Providers, workflow and Crew, plus scoped API-key/token launches for all supported adapters.
3. Persistent browser-login isolation for Claude and Codex, verified against deployed versions; connection-aware transcript/settings helpers.
4. Cursor/Muse browser-login storage verification and Pi OAuth integration with existing per-session isolation.
5. Team/project grants and richer account-specific usage reporting.

## Required verification before release

- Two accounts for each enabled provider can run concurrently without credential crossover.
- Login/logout/reauthentication on account A does not change account B or the administrator login.
- A temporary workspace can be deleted while the selected account remains usable after restart/deployment.
- Structured and tmux paths use the same selected identity; deliberately seeded ambient admin credentials cannot override it.
- Account-specific model inventory, quota, settings and transcripts do not leak through caches or helper paths.
- Same-account conversation resume works; different-account resume is rejected or creates a new conversation explicitly.
- Pi/Muse exclusive MCP settings survive account selection without cross-run settings races.
- Shared-workflow viewers, background agents and schedules cannot use private connections without authorization.
- Revocation/rotation, expired login, missing binary and unavailable connections fail clearly without account fallback.
- Existing RTS/global deployments retain their previous account and policy after migration.

## Evidence and remaining provider verification

Claude directory-based credential storage and credential precedence are documented in [Claude authentication](https://code.claude.com/docs/en/authentication). Cursor documents browser and API-key methods in [Cursor CLI authentication](https://docs.cursor.com/en/cli/reference/authentication).

Codex home/profile behavior, Pi session directory overrides, and Muse settings isolation above are observations from local adapter code. Browser credential-store behavior for each deployed version, concurrent refresh behavior, and isolation against user-executed tools require provider-specific qualification. The implementation and verification sections below distinguish covered paths from remaining work.

## Core account implementation (September 2026)

The header's coding-provider panel supports named private connections alongside the installation's Admin-managed account. Workflow defaults, explicit role bindings, execution-step overrides, and Crew selections save `connection_id`. This is an additive schema-2 field for existing manifests. A missing ID retains legacy resolution; an explicit ID is authorized against the execution user and selected provider and cannot silently fall back to the server account.

| Coding provider | Private authentication |
| --- | --- |
| Claude Code | Subscription OAuth token from `claude setup-token` |
| Codex CLI | API key or guided device/browser login; account-specific `CODEX_HOME` with file credential storage |
| Cursor CLI | API key |
| Pi CLI | API key for one underlying Pi provider; mismatched model families are rejected |
| Muse CLI | Meta API key or guided browser login; account-specific XDG config/data roots |

Browser login for Claude/Cursor needs a separately verified macOS keychain strategy. Direct API-provider account forms, Azure/Bedrock structured credentials, account-specific model discovery, and credential/expiry status remain follow-up work. Saving a key does not verify that the provider accepts it.

The encrypted registry is `config/provider-connections.json`. Private CLI state is stored outside workflow directories at:

```text
~/.local/state/agentworks/provider-connections/<connection-id>/home/
```

Child processes receive account-specific HOME/XDG/Codex/Claude paths. Ambient CLI keys are removed before selected credentials are injected. Account paths participate in retained-session fingerprints, continuation handles retain the connection ID, and backend transcript readers use the same account paths. Existing Crew chats keep their account; a changed selection applies to the Builder/new chats.

Claude's setup helper writes `$CLAUDE_CONFIG_DIR/.claude.json` for private accounts. This location is confirmed in [Claude's configuration guide](https://code.claude.com/docs/en/mcp-quickstart#find-your-configuration-on-disk).

Existing `LLM_CONFIG_LOCKED` behavior remains the default. A deployment can allow private accounts while locking shared configuration with:

```dotenv
ALLOW_PERSONAL_PROVIDER_CONNECTIONS=true
```

Ownership checks apply to account storage, setup, execution, rotation, and removal. Removing a record prevents future credential resolution; cancel an already-running workflow separately. Account selection and normal CLI storage isolation do not provide an OS-user/container boundary for arbitrary native shell execution. Registry mutations currently assume one backend process; multiple replicas require transactional storage.

### Account verification and real P0 tests

Deterministic tests cover encrypted storage, multiple same-provider accounts, owner-only listing/deletion, mismatched/missing/deleted/locked bindings, Codex file credentials, concurrent child environments, session fingerprints, and explicit setup/transcript paths. Frontend tests cover Crew persistence and chat request propagation.

The existing real two-step MCP workflow P0 runner has an opt-in account matrix: A, B, then A again, with different workflow directories and persistent per-account test homes. Set two dedicated keys/tokens for each selected provider:

```text
CODING_P0_CLAUDE_CODE_A_KEY / CODING_P0_CLAUDE_CODE_B_KEY
CODING_P0_CODEX_CLI_A_KEY / CODING_P0_CODEX_CLI_B_KEY
CODING_P0_CURSOR_CLI_A_KEY / CODING_P0_CURSOR_CLI_B_KEY
CODING_P0_PI_CLI_A_KEY / CODING_P0_PI_CLI_B_KEY
CODING_P0_MUSE_CLI_A_KEY / CODING_P0_MUSE_CLI_B_KEY
```

Pi's fixture uses Google/Gemini. Supply fixtures through the existing P0 environment setup, then run:

```bash
CODING_CLI_P0_ACCOUNTS=1 ./scripts/run-coding-cli-p0.sh
```

Missing/identical fixtures fail the live account matrix. It uses real CLI execution and the existing MCP workflow assertions; model prose is not treated as account-identity evidence. HTTP lifecycle tests separately cover authorization/storage. Two live subscription identities, retained direct input under both identities, browser refresh/revocation, and the full browser account-form journey still require live certification.

## Builder provider-switch incident tracking

[PLAT-099](../bugs/pulse_platform/coding-agents/sessions/plat-099.md) tracks the
2026-09-18 Confida recurrence: compare selected provider/model/account before
retained delivery, carry the manifest account ID into runtime construction, and
resolve the workflow workspace before durable receipt comparison. PLAT-324
tracks the related persistence/retry boundary. Provider-switch live acceptance
remains pending; an uncertain submission must never be automatically resent.
