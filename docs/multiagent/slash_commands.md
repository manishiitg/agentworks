# Slash Commands System

Slash commands are quick actions triggered by typing `/` in the chat input. Commands come from three sources, merged by the registry (`frontend/src/commands/registry.ts`):

- **Product commands** (`source: 'product'`): declared in a product's `product.yaml` under `commands:`, with the prompt inline or in a `commands/*.md` file. This is where builder slash commands live (`agent_go/internal/agentworksproduct/product.yaml`). The owning surface registers them on mount via `setProductCommands()` and clears them on unmount.
- **Built-in commands** (`source: 'builtin'`): hardcoded in `frontend/src/commands/builtin-commands.tsx`. Only `/pulse` remains — it is an async frontend API call no static prompt can express.
- **User commands** (`source: 'user'`): custom prompt shortcuts stored in `workspace-docs/commands/custom/`.

## Overview

- **Trigger**: Type `/` in the chat input to open the command picker dialog.
- **Registry**: A unified command registry (`frontend/src/commands/`) merging product, built-in, and user commands.

## Command Registry Architecture

All commands share a single `CommandDefinition` interface:

```typescript
interface CommandDefinition {
  command: string           // Slash command name (e.g. "design-plan")
  aliases?: string[]        // Compatibility names resolving to this command, without menu entries
  searchTerms?: string[]    // Searchable context for a command whose choices live in a picker
  description: string       // Shown in the picker dialog
  icon: ReactNode           // Lucide icon
  modes?: ModeCategory[]    // If set, only visible in these modes (empty = all)
  requiredWorkflowMode?: 'plan' | 'eval' | 'output'
  requiredWorkshopMode?: WorkshopMode | WorkshopMode[]
  validate?: (ctx: CommandContext) => string | null
  hidden?: boolean          // Executable but not shown in picker (e.g. "compact")
  menuHidden?: boolean      // Retained shortcut: executable under access checks, without a menu row
  source: 'builtin' | 'user' | 'product'
  execute: (ctx: CommandContext) => void
}
```

The `CommandContext` provides everything an execute function needs:

```typescript
interface CommandContext {
  beforeSlash: string           // Text typed before the / trigger
  activeTabId: string
  tabSessionId: string | null
  tabConfig: any
  isSummarizing: boolean
  isStreaming: boolean
  onSubmit: (msg: string) => void
  openDialog: (name: string) => void
  openResumeDialog?: () => void
  setTabConfig: (tabId: string, config: any) => void
  addToast: (msg: string, type: 'success' | 'error' | 'info') => void
  handleSummarize: (ctx?: string) => void
  handleCompact: (ctx?: string) => void
  getAppStore: () => any
  getWorkspaceStore: () => any
  getWorkflowStore: () => any
  workflowMode?: 'plan' | 'eval' | 'output'
  workshopMode?: 'workshop' | 'run'
  workflowPhaseId?: string
}
```

### Registry API

```typescript
// Get all visible commands, optionally filtered by chat mode and workshop mode
getCommands(mode?: ModeCategory, workshopMode?: WorkshopMode): CommandDefinition[]

// Find a visible command by name for the current mode
findCommand(name: string): CommandDefinition | undefined

// Replace user commands (called after loading from API)
setUserCommands(cmds: CommandDefinition[]): void

// Fetch user commands from API and register them
loadAndRegisterUserCommands(): Promise<void>

// Register the active product's manifest-owned commands (called by the
// owning surface on mount; cleared with an empty list on unmount)
setProductCommands(cmds: CommandDefinition[]): void
```

## Product Commands: Workflow Chat

Builder slash commands are declared in `agent_go/internal/agentworksproduct/product.yaml` under `commands:`, with prompts in `commands/*.md` next to it. The workflow surface fetches them from the agentworks agent profile and registers them via `setProductCommands()`; the adapter (`frontend/src/commands/agentworksProductCommands.tsx`) owns the shared workflow-mode semantics (workflow mode, plan phase, workshop gate) and the `{{context}}` substitution. Kind-backed commands submit a message asking the agent to call `get_workflow_command_guidance` with the matching `kind`, and the backend returns the canonical guided-flow text from `agent_go/cmd/server/guidance/templates/`.

| Command | Aliases | Backend Kind |
|---------|---------|--------------|
| `/design-plan` | | `design-plan` |
| `/run-plan-drift` (hidden; Pulse tab button) | | `run-plan-drift` |
| `/design-dashboard` | | `design-dashboard` |
| `/setup-goals` | | `setup-goals` |
| `/run-goal-work` | `/strategy-auditor`, `/goal-advisor` | `strategy-auditor` |
| `/run-technical-review` (hidden; Pulse tab button; + 7 hidden `pulse-review-*` focus shortcuts) | `/pulse-review` | `engineering-review` then `pulse-fixer` |
| `/run-architecture-review` (hidden; Pulse tab button) | | prompt only (`references/architecture-review.md`) |
| `/review-code` | | `design-plan` (architecture focus) |
| `/backup` | | prompt only |
| `/publish` | | prompt only |
| `/notify` | | prompt only |
| `/pulse` (hardcoded builtin, not yaml) | | scheduler API call |

There is no `/ops-review`, `/improve-report`, `/improve-knowledge`, `/improve-learnings`, or `/improve-database` command. `ops-review` and `specialize-advisors` are `builder-reference` docs loaded by review turns that need them, not commands; the `improve-*` checklists were removed.

The workflow command source of truth is split across:

- Product manifest: `agent_go/internal/agentworksproduct/product.yaml` + `commands/*.md`
- Product adapter: `frontend/src/commands/agentworksProductCommands.tsx`
- Backend guidance kind registry: `agent_go/cmd/server/guidance/guidance.go`
- Backend guidance templates: `agent_go/cmd/server/guidance/templates/**/<kind>.md`

When adding or removing a workflow guidance command, keep those places in sync. The manifest test (`agent_go/internal/agentworksproduct/commands_test.go`) locks the yaml contract.

## Adding a New Product Command

Add an entry under `commands:` in the product's `product.yaml` and a prompt file next to it:

```yaml
- name: my-command
  description: Does something useful
  icon: terminal
  aliases: [my-alias]       # optional
  menu_hidden: true         # optional: executable without a menu row
  file: commands/my-command.md
```

`{{context}}` in the prompt is replaced with whatever the user typed before the slash. For workflow guidance commands, also add the backend guidance kind in `agent_go/cmd/server/guidance/guidance.go` and the markdown template under `agent_go/cmd/server/guidance/templates/`. Only commands no static prompt can express (today: `/pulse`, an async API call) belong in `frontend/src/commands/builtin-commands.tsx`.

## User-Defined Commands

Users can create custom prompt shortcut commands from the UI. These are stored as workspace files and appear alongside built-in commands in the `/` picker.

### Command File Format

Stored at `commands/custom/{name}/COMMAND.md`:

```markdown
---
name: quick-review
description: Review current code changes
icon: eye
modes: []
---
Review my current code changes for bugs, security issues, and performance problems.

{{context}}
```

### Frontmatter Fields

| Field | Required | Description |
|-------|----------|-------------|
| `name` | Yes | Command name (alphanumeric, hyphens, underscores) |
| `description` | Yes | Brief description shown in the command picker |
| `icon` | No | Any Lucide icon name, see lucide.dev/icons (default: `terminal`) |
| `modes` | No | Array of modes to restrict visibility. Empty `[]` = all modes |

### Icons

Any Lucide icon name in kebab-case, for example `ticket`, `git-branch`, `bug` or `rocket` (see lucide.dev/icons). The server refuses names Lucide does not have.

### Context Placeholder

Use `{{context}}` in the prompt template to include any text the user typed before the `/` trigger. For example, if the user types:

```
fix the login bug /quick-review
```

The text `fix the login bug` is substituted into the `{{context}}` placeholder. If no text was typed before the slash, `{{context}}` is replaced with an empty string.

### Creating Commands via UI

1. Type `/` in the chat input to open the command picker
2. Click the **+ New** button in the picker footer
3. Fill in the command editor form:
   - **Name**: Auto-slugified for the folder name
   - **Description**: Shown in the picker
   - **Icon**: Select from the icon grid
   - **Modes**: Toggle which modes the command is visible in (empty = all)
   - **Prompt template**: The message to submit, with optional `{{context}}`
4. Click **Create**

### Editing and Deleting Commands

- Hover over a user command in the picker to reveal **edit** and **delete** buttons
- Editing opens the same form pre-filled with current values
- Deleting removes the command folder from the workspace

## Storage Structure

```
workspace-docs/
├── commands/
│   └── custom/
│       ├── quick-review/
│       │   └── COMMAND.md
│       └── explain-code/
│           └── COMMAND.md
└── ... other workspace files
```

## Backend Implementation

### Package Structure

```
agent_go/pkg/commands/
├── types.go          # Command, CommandFrontmatter, request/response types
├── parser.go         # Parse/serialize YAML frontmatter + markdown
└── discovery.go      # CRUD operations via workspace API
```

### Key Types

```go
type CommandFrontmatter struct {
    Name        string   `yaml:"name" json:"name"`
    Description string   `yaml:"description" json:"description"`
    Icon        string   `yaml:"icon,omitempty" json:"icon,omitempty"`
    Modes       []string `yaml:"modes,omitempty" json:"modes,omitempty"`
}

type Command struct {
    Frontmatter CommandFrontmatter `json:"frontmatter"`
    Content     string             `json:"content"`     // Prompt template after frontmatter
    FolderName  string             `json:"folder_name"`
    FilePath    string             `json:"file_path"`
}
```

### API Endpoints

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/api/commands` | List all user-defined commands |
| POST | `/api/commands` | Create a new command (body: `{ name, content }`) |
| GET | `/api/commands/{name}` | Get a specific command |
| PUT | `/api/commands/{name}` | Update a command's content |
| DELETE | `/api/commands/{name}` | Delete a command folder |

### Workspace API Integration

The commands package reuses `skills.WorkspaceAPIClient` for all file operations (list, read, write, create folder, delete folder). Commands are stored under `commands/custom/` in the workspace.

## Frontend Implementation

### File Structure

```
frontend/src/commands/
├── types.ts                     # CommandDefinition, CommandContext interfaces
├── builtin-commands.tsx          # Hardcoded builtins (/pulse only)
├── agentworksProductData.ts      # Fetch agentworks product.yaml commands
├── agentworksProductCommands.tsx # Adapter: yaml entries to definitions
├── pulse-review-focus.ts         # Review focus options shared by picker and adapter
├── registry.ts                   # getCommands(), findCommand(), setUserCommands(), setProductCommands()
├── user-commands.ts              # Load user commands from API, icon mapping
└── index.ts                      # Re-exports

frontend/src/components/
├── CommandSelectionDialog.tsx          # Slash-command picker
└── commands/CommandEditorDialog.tsx    # Create/edit command modal form

frontend/src/api/commands.ts     # CRUD API client
frontend/src/types/commands.ts   # Backend model types
```

### API Client

```typescript
// frontend/src/api/commands.ts
export const commandsApi = {
  listCommands(): Promise<ListCommandsResponse>
  getCommand(name: string): Promise<UserCommand>
  createCommand(request: CreateCommandRequest): Promise<UserCommand>
  updateCommand(name: string, request: UpdateCommandRequest): Promise<UserCommand>
  deleteCommand(name: string): Promise<void>
}
```

### How User Commands Are Loaded

1. When the command picker dialog opens, `loadAndRegisterUserCommands()` is called
2. This fetches `GET /api/commands` from the backend
3. Each `UserCommand` is converted to a `CommandDefinition` with:
   - Icon mapped from string name to lucide-react component
   - `execute` function that substitutes `{{context}}` and calls `onSubmit()`
4. User commands are registered via `setUserCommands()` and merged with built-in commands

### Component Integration

- **`CommandSelectionDialog.tsx`**: Uses `getCommands(mode, workshopMode)` from the registry. Shows edit/delete buttons on hover for user commands. Has a `New` button in the footer.
- **`ChatInput.tsx`**: Uses `findCommand(name)` to look up and execute commands. Builds a `CommandContext` from component state and passes it to `cmd.execute(ctx)`. Manages the command editor dialog state.

## Usage Flow

1. **Discover**: Type `/` in chat input → command picker appears with all available commands
2. **Filter**: Continue typing to search commands by name or description
3. **Execute**: Select a command → it runs immediately (opens a dialog, submits a message, toggles a setting, etc.)
4. **Create**: Click `+ New` → fill in the editor form → command is saved to workspace and immediately available
5. **Manage**: Hover over user commands to edit or delete them
