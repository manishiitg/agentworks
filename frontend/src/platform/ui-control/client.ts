import { UI_CONTROL_CONTRACT } from './contract.generated'
import { requestWorkflowPlanStepFocus } from '../../utils/workflowPlanFocus'

export type UIContract = { version: number; product: string; views: readonly { id: string; label: string; actions: readonly string[]; targets: readonly string[] | null; target_kind?: string }[] }

export interface UISnapshot { view: string; revision: number; visible: boolean; target?: string }
export interface UIAction {
  request_id: string
  view: string
  action: string
  target?: string
  expected_state_revision?: number
  expires_at: string
}

// A closed presentation adapter, deliberately NOT a click/selector API. Only
// first-party semantic markers are inspected, never text or credential values.
export function supportedAction(a: UIAction, contract: UIContract = UI_CONTROL_CONTRACT): boolean {
  const view = contract.views.find(v => v.id === a.view)
  if (!view || !(view.actions as readonly string[]).includes(a.action)) return false
  const targetKind = 'target_kind' in view ? view.target_kind : undefined
  return (a.action === 'open' || a.action === 'refresh')
    ? (!a.target || (targetKind === 'view_section'
      ? (view.targets ?? []).includes(a.target)
      : ((targetKind === 'plan_step_id' || targetKind === 'report_tab' || targetKind === 'workspace_file_path') && !!a.target.trim() && a.target.length <= 1024)))
    : (view.targets ?? []).includes(a.target ?? '')
}

export function workspaceHost(workspace: string): HTMLElement | undefined {
  const hosts = Array.from(document.querySelectorAll<HTMLElement>('[data-ui-workspace]'))
    .filter(el => el.dataset.uiWorkspace === workspace)
  return hosts.find(el => el.getClientRects().length > 0 && el.querySelector('[data-ui-view-mounted]'))
    ?? hosts.find(el => el.getClientRects().length > 0)
    ?? hosts[0]
}

export async function applyUIAction(
  action: UIAction, workspace: string, open: (view: string, target?: string) => void,
  state: () => UISnapshot, signal: AbortSignal, refresh?: (view: string, target?: string) => void, contract: UIContract = UI_CONTROL_CONTRACT,
): Promise<{ status: string; code: string }> {
  if (!supportedAction(action, contract)) return { status: 'rejected', code: 'target_not_found' }
  if (signal.aborted || !workspaceHost(workspace)) return { status: 'cancelled', code: 'inactive_scope' }
  if (action.expected_state_revision !== undefined && action.expected_state_revision !== state().revision)
    return { status: 'rejected', code: 'stale_state' }
  const expires = Date.parse(action.expires_at)
  if (!Number.isFinite(expires) || expires <= Date.now()) return { status: 'failed', code: 'timeout' }
  if (action.action === 'refresh') {
    if (!refresh) return { status: 'rejected', code: 'unsupported_action' }
    refresh(action.view, action.target)
  } else open(action.view, action.target)
  // Observe actual mount/visibility, including lazy-loaded inspectors. Bounded;
  // listeners/timers are removed on every terminal outcome and on unmount.
  return new Promise(resolve => {
    let done = false
    let touched = false
    let requestedNode: Element | undefined
    const finish = (status: string, code = '') => {
      if (done) return
      done = true
      clearTimeout(timer)
      observer.disconnect()
      signal.removeEventListener('abort', aborted)
      document.removeEventListener('pointerdown', interrupted, true)
      document.removeEventListener('keydown', interrupted, true)
      document.removeEventListener('wheel', interrupted, true)
      resolve({ status, code })
    }
    const aborted = () => finish('cancelled', 'inactive_scope')
    const interrupted = () => { touched = true; finish('cancelled', 'user_interrupted') }
    const check = () => {
      if (done || touched) return
      if (signal.aborted || document.visibilityState !== 'visible') return aborted()
      const host = workspaceHost(workspace)
      if (!host) return aborted()
      if (host.dataset.uiView !== action.view) return
      if (host.getClientRects().length === 0) return
      // The marker lives INSIDE Suspense, not in its loading fallback.
      if (!host.querySelector('[data-ui-view-mounted]')) return
      if ((action.view === 'mcp' || action.view === 'memory') && action.target) {
        const section = host.querySelector<HTMLElement>('[data-ui-view-section]')
        if (section?.dataset.uiViewSection !== action.target || section.getClientRects().length === 0) return
      }
      if (action.view === 'flow' && action.target) {
        // Wait for the actual node, including lazy canvas/data loading. Never
        // interpolate a caller's target into a selector or synthesize clicks.
        const node = Array.from(host.querySelectorAll('.react-flow__node[data-id]'))
          .find(el => el.getAttribute('data-id') === action.target)
        if (!node) return
        if (requestedNode !== node) {
          requestedNode = node
          requestWorkflowPlanStepFocus({ workspacePath: workspace, stepId: action.target })
        }
        const panel = Array.from(host.querySelectorAll<HTMLElement>('[data-ui-plan-step]'))
          .find(el => el.dataset.uiPlanStep === action.target)
        if (!panel || panel.getClientRects().length === 0) return
      }
      if (action.view === 'files' && action.target) {
        // Opening the Files shell is not success: wait until the requested file
        // has actually loaded in the right-side viewer. The marker is semantic
        // first-party state, not a caller-controlled selector.
        const viewer = host.querySelector<HTMLElement>('[data-ui-file-path]')
        const shownPath = viewer?.dataset.uiFilePath ?? ''
        const requestedPath = action.target.replace(/^\/+|\/+$/g, '')
        if (!viewer || (shownPath !== requestedPath && !shownPath.endsWith(`/${requestedPath}`))) return
        if (viewer.dataset.uiFileReady !== 'true' || viewer.getClientRects().length === 0) return
      }
      if (action.action === 'expand') {
        const details = Array.from(host.querySelectorAll<HTMLDetailsElement>('details[data-ui-instructions]'))
          .find(el => el.dataset.uiInstructions === action.target)
        if (!details) return
        details.open = true
        details.scrollIntoView({ block: 'nearest', behavior: 'instant' })
        if (!details.open || details.getClientRects().length === 0) return
      }
      finish('applied')
    }
    const observer = new MutationObserver(check)
    const timer = setTimeout(() => finish('failed', action.target ? 'target_not_found' : 'render_failed'), Math.min(8000, expires - Date.now()))
    observer.observe(document.body, { subtree: true, childList: true, attributes: true, attributeFilter: ['data-ui-view', 'data-ui-view-section', 'data-ui-plan-step', 'data-ui-file-path', 'data-ui-file-ready', 'open', 'class'] })
    signal.addEventListener('abort', aborted, { once: true })
    document.addEventListener('pointerdown', interrupted, true)
    document.addEventListener('keydown', interrupted, true)
    document.addEventListener('wheel', interrupted, { capture: true, passive: true })
    requestAnimationFrame(check)
  })
}
