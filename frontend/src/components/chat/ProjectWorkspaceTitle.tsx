import { useCallback, useRef } from 'react'
import { ChevronDown } from 'lucide-react'
import { ProductSurfaceIcon } from '../ProductSurfaceIcon'
import type { ProductSurface } from '../../products/productSurfaceConfig'
import { useProductNavigationToolbarStore } from '../../stores/useProductNavigationToolbarStore'

/** Project identity stays visible while people switch between its chats. */
export function ProjectWorkspaceTitle({ name, surface }: { name: string; surface: ProductSurface }) {
  const trigger = useRef<HTMLButtonElement | null>(null)
  const pickerOpen = useProductNavigationToolbarStore(state => state.pickerOpen)
  const register = useCallback((node: HTMLButtonElement | null) => {
    const previous = trigger.current
    trigger.current = node
    if (node || useProductNavigationToolbarStore.getState().workspaceTrigger === previous)
      useProductNavigationToolbarStore.setState({ workspaceTrigger: node })
  }, [])
  return <button ref={register} type="button" data-workspace-setup-trigger data-project-title
    aria-label={`Switch workspace: ${name}`} aria-haspopup="menu" aria-expanded={pickerOpen} title={name}
    onClick={() => window.dispatchEvent(new Event('open-workspace-picker'))}
    className="mr-2 inline-flex h-10 min-w-0 max-w-[30vw] md:h-8 shrink-0 items-center gap-2 rounded-md px-1 text-sm font-semibold hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring md:max-w-56">
    <ProductSurfaceIcon surface={surface} className="h-4 w-4 shrink-0" />
    <span className="truncate">{name}</span><ChevronDown className="h-3 w-3 shrink-0 text-muted-foreground" aria-hidden="true" />
  </button>
}
