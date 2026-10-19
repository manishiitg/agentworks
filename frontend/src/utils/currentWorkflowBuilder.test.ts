import { expect, it } from 'vitest'
import { isCurrentWorkflowBuilderTab } from './currentWorkflowBuilder'
import type { ChatTab } from '../stores/useChatStore'
import type { WorkflowBuilderSessionResponse } from '../services/api-types'
const binding: WorkflowBuilderSessionResponse = { success: true, source: 'live', status: 'running', events: [], session_id: 'current', is_builder: true, sessions: {
  current: { is_builder: true, kind: 'builder' }, old: { is_builder: false, kind: 'history' },
  pulse: { is_builder: false, kind: 'run' },
} }
const tab = (id: string, metadata = {}): ChatTab => ({ tabId: id, sessionId: id,
  lastAccessedAt: 9999999999999, metadata: { mode: 'workflow', phaseId: 'workflow-builder', ...metadata },
}) as ChatTab
it('uses the server marker, never browser access time or clock, for Builder notices', () => {
  expect(isCurrentWorkflowBuilderTab(tab('current'), binding)).toBe(true)
  expect(isCurrentWorkflowBuilderTab(tab('old'), binding)).toBe(false)
  expect(isCurrentWorkflowBuilderTab(tab('pulse'), binding)).toBe(false)
  expect(isCurrentWorkflowBuilderTab(tab('current'), undefined)).toBe(false)
})
it('keeps execution and view-only inspectors separate even when inspecting the current Builder', () => {
  expect(isCurrentWorkflowBuilderTab(tab('current', { isViewOnly: true }), binding)).toBe(false)
  expect(isCurrentWorkflowBuilderTab(tab('current', { isExecutionRun: true }), binding)).toBe(false)
})
