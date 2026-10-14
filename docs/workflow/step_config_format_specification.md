# step_config.json Format Specification

## 📋 Overview

The `step_config.json` file stores step-specific agent configurations (LLM models, tool selections, execution settings) separate from the main plan structure. This separation allows plan.json to focus on workflow structure while step_config.json manages execution details.

**Key Benefits:**
- **Separation of concerns**: Plan structure vs execution configuration
- **Consistent format**: Object format with `steps` array (both frontend and backend)
- **Backward compatibility**: Legacy array format supported during migration

---

## 📁 Key Files & Locations

| Component | File Path | Key Functions |
|-----------|-----------|---------------|
| **Frontend Parser** | [`frontend/src/components/workflow/hooks/usePlanData.ts`](../../frontend/src/components/workflow/hooks/usePlanData.ts) | `normalizeStepConfigFile()`, `saveStepConfig()` |
| **Backend Parser** | [`agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/step_config.go`](../../agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/step_config.go) | `ParseStepConfigContent()`, `ReadStepConfigs()`, `WriteStepConfigs()` |
| **Type Definitions** | [`frontend/src/utils/stepConfigMatching.ts`](../../frontend/src/utils/stepConfigMatching.ts) | `StepConfig`, `AgentConfigs` |
| **Backend Types** | [`agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/planning_agent.go`](../../agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/planning_agent.go) | `AgentConfigs` struct definition |

## 📂 File Locations

The `step_config.json` file can exist in two locations (with priority):

1. **Run-specific config** (highest priority): `{workspacePath}/runs/{runFolder}/planning/step_config.json`
   - Used when a specific run folder is selected
   - Allows different configs for different execution runs

2. **Default config** (fallback): `{workspacePath}/planning/step_config.json`
   - Used when no run folder is selected or run-specific config doesn't exist
   - Shared across all runs

**File**: [`step_config.go:40-76`](../../agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/step_config.go#L40)

The backend automatically checks run-specific config first, then falls back to default config.

---

## 🔄 Format Structure

### Object Format (Only Supported Format)

Both frontend and backend **read and write** only the object format with `steps` field:

```json
{
  "steps": [
    {
      "id": "step-id-here",
      "title": "Optional step title",
      "agent_configs": {
        "selected_servers": ["server1", "server2"],
        "selected_tools": ["server1:tool1", "server1:tool2", "server2:*"],
        "enabled_custom_tools": ["workspace_browser:agent_browser"],
        "execution_llm": {
          "provider": "openai",
          "model_id": "gpt-4o"
        },
        "use_code_execution_mode": true,
        "disable_validation": false,
        "llm_validation_mode": "skip",
        "learnings_access": "read-write",
        "learning_objective": "Capture target-system selectors, auth flow quirks, and session-expiry signals",
        "enable_context_offloading": true,
        "enable_prerequisite_detection": true,
        "prerequisite_rules": [
          {
            "depends_on_step": "step-0",
            "description": "If login session is missing or expired, go back to step 0"
          }
        ]
      }
    }
  ]
}
```

### Field Structure

| Field | Required | Type | Description |
|-------|----------|------|-------------|
| `steps` | ✅ | `array` | Root array containing step configurations |
| `steps[].id` | ✅ | `string` | Stable step ID from plan.json |
| `steps[].title` | ❌ | `string` | Step title for reference/display only |
| `steps[].agent_configs` | ❌ | `object` | Nested configuration object |

---

## 🔄 Implementation Details

### Frontend Implementation

**File:** [`frontend/src/components/workflow/hooks/usePlanData.ts`](../../frontend/src/components/workflow/hooks/usePlanData.ts)

**Read:**
```typescript
function normalizeStepConfigFile(rawContent: unknown): StepConfig[] {
  // Handles object format: { "steps": [...] }
  if (rawContent && typeof rawContent === 'object' && !Array.isArray(rawContent)) {
    const obj = rawContent as Record<string, unknown>
    if ('steps' in obj && Array.isArray(obj.steps)) {
      return obj.steps as StepConfig[]
    }
  }
  // Legacy array format support (with warning)
  if (Array.isArray(rawContent)) {
    return rawContent as StepConfig[]
  }
  return []
}
```

**Write:**
```typescript
const content = JSON.stringify({ steps: stepConfigs }, null, 2)
await agentApi.updatePlannerFile(stepConfigPath, content, `Updated step config for step ${stepId}`)
```

### Backend Implementation

**File:** [`agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/step_config.go`](../../agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/step_config.go)

**Read:**
```go
type StepConfigFile struct {
    Steps []StepConfig `json:"steps"`
}

func ParseStepConfigContent(content string) ([]StepConfig, error) {
    var file StepConfigFile
    if err := json.Unmarshal([]byte(content), &file); err != nil {
        return nil, err
    }
    return file.Steps, nil
}
```

**Write:**
```go
file := StepConfigFile{Steps: configs}
content, err := json.MarshalIndent(file, "", "  ")
```

---

## ⚙️ Configuration Fields

**File**: [`planning_agent.go`](../../agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/planning_agent.go) - `AgentConfigs` struct

### LLM Configuration

| Field | Type | Default | Purpose |
|-------|------|---------|---------|
| `execution_llm` | `object` | Preset default | LLM config for execution agent (`{ provider: string, model_id: string }`) |
| `validation_llm` | `object` | Preset default | LLM config for validation agent |

### Turn Limits

Per-step and workflow-default turn-limit properties are retired. Legacy `execution_max_turns` values are ignored on load and omitted from typed config saves; execution uses the shared runtime limit.

### Tool & Server Configuration

| Field | Type | Default | Purpose |
|-------|------|---------|---------|
| `selected_servers` | `string[]` | `[]` | MCP servers to use for this step (subset of preset servers) |
| `selected_tools` | `string[]` | `[]` | Specific tools (format: `"server:tool"` or `"server:*"` for all tools) |
| `enabled_custom_tools` | `string[]` | `[]` | Optional additions in `"category:tool"` or `"category:*"` format. Execution agents automatically receive `workspace_advanced` plus `human_feedback`, `create_human_input_request`, `send_email` and `send_slack_message`. Name any additional `human_tools` entry explicitly. Legacy `human_tools:*` is normalized to the same baseline. |
| `enabled_custom_tool_categories` | `string[]` | `[]` | **Legacy format**: Tool categories (e.g., `["workspace_tools", "human_tools"]`) - deprecated, use `enabled_custom_tools` instead |

### Validation Configuration

| Field | Type | Default | Purpose |
|-------|------|---------|---------|
| `disable_validation` | `boolean` | `true` (nil = disabled) | LLM validation control: `nil`/`true` = disabled (auto-approve), `false` = enabled. Pre-validation always runs if schema exists. |
| `llm_validation_mode` | `"skip"\|"auto"\|"always"` | `"skip"` | Validation strategy: `"skip"` (trust pre-validation), `"auto"` (skip after 3 successes), `"always"` (standard) |

### Learning Configuration

| Field | Type | Default | Purpose |
|-------|------|---------|---------|
| `learnings_access` | `"read"\|"read-write"\|"none"` | `"read"` (auto-migrated from `learning_objective` if unset) | Primary gate for the global learnings store. `"read"` — step sees `learnings/_global/SKILL.md` in its prompt; `"read-write"` — step also contributes (requires non-empty `learning_objective`); `"none"` — step neither reads nor contributes. Mirrors `knowledgebase_access`. |
| `learning_objective` | `string` | `""` | Extraction instruction for the post-step learning agent — describes what patterns/selectors/recipes should land in `SKILL.md`. Required when `learnings_access="read-write"`; the validator rejects the combination of write access with an empty objective. |

### Execution Mode Configuration

| Field | Type | Default | Purpose |
|-------|------|---------|---------|
| `use_code_execution_mode` | `boolean` | Preset default (nil = use preset) | Step-level code execution mode override (nil = use preset default, true/false = override) |
| `enable_context_offloading` | `boolean` | `true` (nil = enabled) | Enable/disable context offloading virtual tools |

### Prerequisite Detection Configuration

| Field | Type | Default | Purpose |
|-------|------|---------|---------|
| `enable_prerequisite_detection` | `boolean` | `false` | Enable prerequisite failure detection for this step |
| `prerequisite_rules` | `array` | `[]` | Array of prerequisite rules, each with `depends_on_step` (string) and `description` (string) |

## 🛠️ Common Issues & Solutions

| Issue | Cause | Solution |
|-------|-------|----------|
| `"steps" field not found` | File uses legacy array format | Use `normalizeStepConfigFile()` to handle both formats (frontend) or ensure backend uses object format |
| `Step ID mismatch` | Step ID doesn't match plan.json | Ensure step IDs are stable and match plan.json exactly |
| `agent_configs is null` | Step has no configuration | This is valid - step will use preset defaults |
| `Parse error` | Invalid JSON structure | Verify file uses object format: `{ "steps": [...] }` |
| `Config not found` | Run-specific config missing | Backend automatically falls back to default config in `{workspacePath}/planning/step_config.json` |
| `enabled_custom_tool_categories` ignored | Using legacy format | Migrate to unified `enabled_custom_tools` format: `"category:tool"` or `"category:*"` |

---

## 🔍 For LLMs: Quick Reference

**Constraints:**
- ✅ **Allowed**: Object format `{ "steps": [...] }`
- ✅ **Allowed**: Empty `steps` array `[]`
- ✅ **Allowed**: Missing `agent_configs` (uses defaults)
- ❌ **Forbidden**: Root-level array format (legacy, deprecated)
- ❌ **Forbidden**: Missing `id` field in step config

**Example:**
```json
{
  "steps": [
    {
      "id": "step-1",
      "agent_configs": {
        "selected_servers": ["aws"],
        "execution_llm": {
          "provider": "openai",
          "model_id": "gpt-4o"
        }
      }
    }
  ]
}
```

**File Operations:**
- **Read**: Always parse object format, extract `steps` array
- **Write**: Always write object format: `JSON.stringify({ steps: stepConfigs }, null, 2)`
- **Update**: Find step by `id`, update `agent_configs`, save entire file
- **Config Priority**: Run-specific config (`runs/{runFolder}/planning/step_config.json`) takes precedence over default config (`planning/step_config.json`)

**Field Notes:**
- All boolean fields use `nil` (undefined) to mean "use default/preset value"
- `enabled_custom_tools` uses unified format: `"category:tool"` or `"category:*"`. Current categories include `workspace_advanced`, `workspace_tools`, `workspace_browser`, and `human_tools`; use `execute_shell_command` / `diff_patch_workspace_file` for file access instead of legacy basic workspace tool names.
- `learning_detail_level` accepts `"exact"`, `"general"`, or `"none"` (default: `"exact"`)

---

## 📖 Related Documentation

- [Workflow Docs](README.md) - Overall workflow architecture
- [Learn Code and Code Execution Modes](learn_code_flow.md) - Scripted execution configuration and flow
- [Step Config Matching](../../frontend/src/utils/stepConfigMatching.ts) - Type definitions and matching logic
