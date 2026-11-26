// Shared types for all stores

// Re-export API types for convenience
export type { PlannerFile, PollingEvent, LLMConfiguration } from '../services/api-types'

// MCP Server Types
export interface ToolDefinition {
  name: string
  description: string
  parameters: Record<string, unknown>
  status?: string
  /** Connection ownership: 'connected' | 'available' — "is this server mine?".
   *  Distinct from `status`, which reports reachability. A connected server
   *  that is down is 'connected' with status 'error'. */
  connection?: string
  error?: string
  server?: string
  toolsEnabled?: number
  function_names?: string[]
  tools?: ToolDetail[]
}

export interface ToolDetail {
  name: string
  description: string
  server: string
  parameters?: Record<string, {
    description?: string
    type?: string
  }>
  required?: string[]
}

// File Context Types
export interface FileContextItem {
  context?: { kind: 'browser' | 'terminal'; label: string; previewPath?: string }
  admittedAttachment?: { hash: string; name: string }
  name: string
  path: string
  type: 'file' | 'folder'
}

// Chat Session Types
export interface ChatSession {
  id: string
  title: string
  createdAt: number
  lastActivity: number
}

// UI State Types
export interface Toast {
  id: string
  message: string
  type: 'success' | 'info' | 'error' | 'warning'
}

// Agent Mode Types
// Canonical values:
//   - 'multi-agent' — tool-backed chat (was 'simple' before consolidation).
//   - 'workflow' — workflow run.
// 'simple' is accepted as a legacy alias on the backend boundary; new code
// uses 'multi-agent' consistently.
export type AgentMode = 'multi-agent' | 'workflow'

// Workflow Types - dynamic from backend
export type WorkflowPhase = string

// Store Action Types
export interface StoreActions {
  // Generic actions that all stores might need
  reset: () => void
  setLoading: (loading: boolean) => void
  setError: (error: string | null) => void
}
