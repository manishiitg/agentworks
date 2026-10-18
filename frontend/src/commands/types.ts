import type { ReactNode } from 'react'
import type { ModeCategory } from '../stores/useModeStore'
import type { ExecutionOptions } from '../services/api-types'
import type { ChatTabConfig, useChatStore } from '../stores/useChatStore'
import type { useAppStore } from '../stores/useAppStore'
import type { useWorkspaceStore } from '../stores/useWorkspaceStore'
import type { useWorkflowStore } from '../stores/useWorkflowStore'
import type { DialogName } from '../stores/useCommandDialogStore'

type AppStoreState = ReturnType<typeof useAppStore.getState>
type WorkspaceStoreState = ReturnType<typeof useWorkspaceStore.getState>
type WorkflowStoreState = ReturnType<typeof useWorkflowStore.getState>

// Visible workshop modes in the main workshop UI:
//   - 'builder'   — design the workflow plan, step config, and live report
//   - 'optimizer' — run / eval / harden existing steps until reliable
//
// Historical: 'eval' and 'output' modes folded into 'builder' in an earlier
// migration; 'ask' (formerly 'debugger') folded into 'run'. 'reporting' is
// still accepted for backend compatibility but the UI maps report authoring to
// Builder. 'run' remains in the type for backend/bot routes such as Slack and
// WhatsApp, but it is not shown in the main workshop mode toggle.
// WorkshopMode values (post-merge):
//   - 'workshop' — the unified design+run+harden+replan+report mode.
//   - 'run' — read-mostly runtime (Slack/WhatsApp/deployed use).
//
// Legacy modes ("builder", "optimizer", "reporting") were merged into
// "workshop" in the prompt-restructure migration. Persisted sessions with
// the old names are normalized on the backend; the frontend never receives
// them as canonical values (loadtime migration in migrateWorkshopMode).
export type WorkshopMode = 'workshop' | 'run'

export interface CommandContext {
  beforeSlash: string
  // Set for a specific product's tab (e.g. 'video-studio'); unset for the
  // product-owned chat surface. Lets a command opt out of surfaces where it
  // makes no sense.
  agentProfileId?: string
  activeTabId: string
  tabSessionId: string | null
  tabConfig: ChatTabConfig | undefined
  isStreaming: boolean
  onSubmit: (msg: string) => void
  setInputText: (text: string) => void
  openDialog: (name: DialogName) => void
  setTabConfig: ReturnType<typeof useChatStore.getState>['setTabConfig']
  addToast: (msg: string, type: 'success' | 'error' | 'info') => void
  submitWithExecutionOptions?: (msg: string, executionOptions?: ExecutionOptions) => void
  getAppStore: () => AppStoreState
  getWorkspaceStore: () => WorkspaceStoreState
  getWorkflowStore: () => WorkflowStoreState
  modeCategory?: ModeCategory
  workflowMode?: 'plan' | 'eval' | 'output'
  workshopMode?: WorkshopMode
  // Resolved from the active chat workflow; shared with command permission checks.
  workflowWorkspacePath?: string
  workflowPhaseId?: string
}

export interface CommandDefinition {
  command: string
  // Compatibility names resolve to this command without adding menu entries.
  aliases?: string[]
  // Searchable context for a command whose choices live in a picker.
  searchTerms?: string[]
  description: string
  icon: ReactNode
  modes?: ModeCategory[]
  requiredWorkflowMode?: 'plan'
  requiredWorkshopMode?: WorkshopMode | WorkshopMode[]
  validate?: (ctx: CommandContext) => string | null
  hidden?: boolean
  // Retained shortcuts can execute under normal access checks without menu rows.
  menuHidden?: boolean
  // 'product' commands ship with the active product (declared in its
  // product.yaml) rather than being hardcoded platform builtins or the
  // user's own markdown -- they are not user-editable, which the command
  // dialog keys off when deciding whether to offer edit/delete.
  source: 'builtin' | 'user' | 'product'
  execute: (ctx: CommandContext) => void
}
