import type { ReactNode } from 'react'
import { openProductWorkspace } from '../../utils/productWorkspaceNavigation'
import { openAppProduct } from '../../utils/appNavigation'
import { ChevronDown } from 'lucide-react'
import { useAuthStore } from '../../stores/useAuthStore'
import { sidebarExpansionKey, useSidebarExpansionStore } from '../../stores/useSidebarExpansionStore'
import { EntityIdentityIcon } from '../ui/EntityIdentityIcon'
import { ProductSurfaceIcon } from '../ProductSurfaceIcon'

export function SidebarServerGroup({ id, name, children, actions }: { id: string; name: string; children?: ReactNode; actions?: ReactNode }) {
  const userId = useAuthStore(state => state.user?.id)
  const key = sidebarExpansionKey(userId || '', 'server', id)
  const expanded = useSidebarExpansionStore(state => state.expanded[key] ?? true)
  const toggle = useSidebarExpansionStore(state => state.toggle)
  return <section aria-label={`${name} projects`} className="border-t border-border/50 pt-3 first:border-t-0 first:pt-1">
    <div className="mb-1 flex items-center gap-1">
      <button type="button" aria-expanded={expanded} onClick={() => toggle(key, true)} className="flex min-w-0 flex-1 items-center gap-2 rounded-md px-2.5 py-2 text-left text-[11px] font-semibold uppercase tracking-widest text-muted-foreground hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
        <span className="min-w-0 flex-1 truncate">{name}</span>
        <ChevronDown aria-hidden="true" className={`h-3 w-3 shrink-0 transition-transform ${expanded ? '' : '-rotate-90'}`} />
      </button>
      {actions}
    </div>
    <div hidden={!expanded} className="space-y-0.5">{children}</div>
  </section>
}

export function SidebarProjectRow({ name, kind, icon, selected, onOpen }: { name: string; kind: 'crew' | 'goal'; icon?: string; selected?: boolean; onOpen: () => void }) {
  const label = kind === 'crew' ? 'Crew' : 'Goal'
  return <button type="button" title={`${label} · ${name}`} aria-label={`${name} · ${label}`} aria-current={selected ? 'page' : undefined} onClick={onOpen}
    className={`flex h-9 w-full items-center gap-2.5 rounded-lg px-2.5 text-left text-[13px] transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${selected ? 'bg-foreground/[0.08] font-medium text-foreground' : 'text-foreground/75 hover:bg-muted hover:text-foreground'}`}>
    <EntityIdentityIcon icon={icon} label={name} className="h-4 w-4" fallback={<ProductSurfaceIcon surface={kind === 'crew' ? 'work' : 'agentworks'} className="h-4 w-4 shrink-0" />} />
    <span className="min-w-0 flex-1 truncate">{name}</span>
  </button>
}

/** Reopen the workspace even when an overlay kept its project URL unchanged. */
export function openSidebarProject(kind: 'crew' | 'goal', id: string) {
  const surface = kind === 'crew' ? 'work' : 'agentworks'
  window.dispatchEvent(new Event('close-app-sidebar'))
  openProductWorkspace(surface)
  openAppProduct({ surface, id })
}
