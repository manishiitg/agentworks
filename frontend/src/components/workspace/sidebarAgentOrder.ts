import { useSyncExternalStore } from 'react'
import { useAuthStore } from '../../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'

const changed = 'agentworks-sidebar-agent-order'
function subscribe(update: () => void) {
  window.addEventListener(changed, update)
  window.addEventListener('storage', update)
  return () => {
    window.removeEventListener(changed, update)
    window.removeEventListener('storage', update)
  }
}

export function useSidebarAgentOrder(projectId?: string) {
  const person = useAuthStore(state => state.user?.id)
  const server = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  const key = projectId && person ? 'agentworks.sidebar.agent-order:' + JSON.stringify([server ?? 'local', person, projectId]) : ''
  const raw = useSyncExternalStore(subscribe, () => {
    try { return key ? localStorage.getItem(key) ?? '' : '' } catch { return '' }
  }, () => '')
  let order: string[] = []
  try {
    const value: unknown = JSON.parse(raw || '[]')
    if (Array.isArray(value)) order = value.filter((item): item is string => typeof item === 'string')
  } catch { /* A broken preference does not prevent showing the list. */ }
  const save = (next: string[]) => {
    if (!key) return false
    try {
      localStorage.setItem(key, JSON.stringify(next))
      window.dispatchEvent(new Event(changed))
      return true
    } catch { return false }
  }
  return { order, save }
}

/** Currently absent rows don't render; their slots stay saved for the next roster. */
export function sidebarRowsInOrder<T>(rows: { id: string; value: T }[], order: string[]) {
  const byId = new Map(rows.map(row => [row.id, row]))
  const ordered = order.flatMap(id => {
    const row = byId.get(id)
    byId.delete(id)
    return row ? [row] : []
  })
  return [...ordered, ...byId.values()]
}

export function moveSidebarRow(order: string[], visible: string[], id: string, before: string | null) {
  if (before === id || !visible.includes(id) || (before !== null && !visible.includes(before))) return order
  const next = visible.filter(key => key !== id)
  next.splice(before === null ? next.length : next.indexOf(before), 0, id)
  const shown = new Set(visible)
  const saved = [...new Set(order)].map(key => shown.has(key) ? next.shift()! : key)
  return [...saved, ...next]
}

export const sidebarGroupId = (name: string) => `group:${name}`
export const sidebarAgentId = (key: string) => `agent:${key}`
