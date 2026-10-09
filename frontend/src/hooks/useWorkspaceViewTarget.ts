import { useEffect, useRef } from 'react'
import { useWorkflowStore } from '../stores/useWorkflowStore'
import type { WorkspaceViewId } from '../components/workflow/workspaceViews'

// The newest target token each view has applied, so a remounted panel does
// not re-apply an old target over a tab the person chose since.
const appliedTokens = new Map<string, number>()

/** Calls apply(target) once for each new target aimed at this view, including
 * one set just before the panel mounted (⌘/Ctrl+K opens a panel on a tab). */
export function useWorkspaceViewTarget(view: WorkspaceViewId, apply: (target: string) => void, enabled = true) {
  const target = useWorkflowStore(state => state.workspaceViewTarget)
  const applyRef = useRef(apply)
  applyRef.current = apply
  useEffect(() => {
    if (!enabled || !target || target.view !== view) return
    if ((appliedTokens.get(view) ?? 0) >= target.token) return
    appliedTokens.set(view, target.token)
    applyRef.current(target.target)
  }, [target, view, enabled])
}

/** Aims a target at a view without switching the workflow pane (Crew and Code). */
export function setWorkspaceViewTarget(view: WorkspaceViewId, target: string) {
  useWorkflowStore.setState(state => ({ workspaceViewTarget: { view, target, token: (state.workspaceViewTarget?.token ?? 0) + 1 } }))
}
