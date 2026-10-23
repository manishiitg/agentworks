import { useEffect, useState } from 'react'
import { create } from 'zustand'
import { agentApi } from '../services/api'
import { useAuthStore } from '../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../stores/useWorkspaceConnectionStore'
import { captureChatIdentity, isChatIdentityCurrent } from '../utils/chatIdentity'
import type { CustomPreset, PredefinedPreset } from '../types/preset'
import type { WorkflowBuilderSessionResponse } from '../services/api-types'
import type { WorkflowBuilderBindings } from '../utils/currentWorkflowBuilder'
import { useLiveRefetch } from './useLiveRefetch'

const EMPTY: WorkflowBuilderBindings = {}
const useBindings = create<{ identity: number; bindings: WorkflowBuilderBindings }>(() => ({ identity: -1, bindings: EMPTY }))
let pending = new Map<string, { preset_query_id: string; workspace_path: string; session_ids?: string[] }>()
let pendingIdentity = -1
let flush: ReturnType<typeof setTimeout> | undefined
let mutation = 0

function requestBindings(presets: (CustomPreset | PredefinedPreset)[], sessions: Record<string, string[]>) {
  const identity = captureChatIdentity()
  if (pendingIdentity !== identity) { pending = new Map(); pendingIdentity = identity }
  for (const preset of presets) {
    if (preset.selectedFolder?.filepath) pending.set(preset.id, { preset_query_id: preset.id, workspace_path: preset.selectedFolder.filepath,
      session_ids: [...new Set([...(pending.get(preset.id)?.session_ids || []), ...(sessions[preset.id] || [])])].sort(),
    })
  }
  // Collect the sidebar and active workspace requests into one batch.
  flush ??= setTimeout(() => {
    flush = undefined
    const identity = pendingIdentity
    const scopes = [...pending.values()]
    pending.clear()
    if (!scopes.length || !isChatIdentityCurrent(identity)) return
    const startedMutation = mutation
    void agentApi.getWorkflowBuilderSessions(scopes).then(response => {
      if (!isChatIdentityCurrent(identity) || startedMutation !== mutation) return
      useBindings.setState(state => ({ identity, bindings: {
        ...(state.identity === identity ? state.bindings : {}),
        ...Object.fromEntries(scopes.flatMap(scope => response.bindings[scope.preset_query_id]
          ? [[scope.preset_query_id, response.bindings[scope.preset_query_id]]] : [])),
      } }))
    }).catch(() => { /* Keep the last server snapshot; live feed retries. */ })
  }, 10)
}

/** Apply a server New chat response, invalidating older in-flight discovery. */
export function workflowBuilderMutation(): number { return mutation }
export function currentWorkflowBuilderBinding(id: string): WorkflowBuilderSessionResponse | undefined {
  const state = useBindings.getState()
  return state.identity === captureChatIdentity() ? state.bindings[id] : undefined
}
export function publishWorkflowBuilderBinding(binding: WorkflowBuilderSessionResponse, identity: number, changed = false): void {
  if (!isChatIdentityCurrent(identity) || !binding.preset_query_id) return
  if (changed) mutation += 1
  useBindings.setState(state => ({ identity, bindings: {
    ...(state.identity === identity ? state.bindings : {}), [binding.preset_query_id!]: binding,
  } }))
}

/** Read the existing shared snapshot; this selector adds no polling. */
export function useWorkflowBuilderBinding(id: string | null): WorkflowBuilderSessionResponse | undefined {
  const identity = captureChatIdentity()
  return useBindings(state => state.identity === identity && id ? state.bindings[id] : undefined)
}

export function useWorkflowBuilderBindings(presets: (CustomPreset | PredefinedPreset)[], sessions: Record<string, string[]> = {}): WorkflowBuilderBindings {
  const userId = useAuthStore(state => state.user?.id)
  const workspace = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  const snapshot = useBindings()
  const identity = captureChatIdentity()
  const [tick, setTick] = useState(0)
  useLiveRefetch(() => setTick(value => value + 1), {
    kinds: ['sessions'], fallbackMs: 30000, safetyMs: 60000, minIntervalMs: 30000, enabled: presets.length > 0,
  })
  const key = presets.map(preset => `${preset.id}:${preset.selectedFolder?.filepath || ''}`).sort().join('|')
  const sessionsKey = JSON.stringify(sessions)
  useEffect(() => {
    requestBindings(presets, sessions)
    // The key describes scopes, rather than array identity.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [identity, userId, workspace, key, sessionsKey, tick])
  return snapshot.identity === identity ? snapshot.bindings : EMPTY
}
