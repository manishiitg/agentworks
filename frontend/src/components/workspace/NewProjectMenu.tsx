import { Plus } from 'lucide-react'
import { useAuthStore } from '../../stores/useAuthStore'
import { useProductSurfaceStore } from '../../stores/useProductSurfaceStore'
import { quickNavigationItems } from '../../utils/quickNavigation'
import { openHome } from '../../utils/homeNavigation'

/** New starts from Home; direct creation stays in its existing checked actions. */
export function NewProjectMenu({ compact = false }: { compact?: boolean }) {
  const user = useAuthStore(state => state.user)
  const multiUser = useAuthStore(state => state.isMultiUserMode)
  const product = useProductSurfaceStore(state => state.productSurface)
  const choices = quickNavigationItems(user, product, multiUser).filter(item => item.action === 'create' && item.projectDescription)
  if (!choices.length) return null
  return <button type="button" aria-label="New project" title="New" onClick={() => {
    const auth = useAuthStore.getState()
    if (quickNavigationItems(auth.user, useProductSurfaceStore.getState().productSurface, auth.isMultiUserMode).some(item => item.action === 'create' && item.projectDescription)) openHome(true)
  }} className={`flex items-center rounded-lg text-[13px] text-foreground/85 transition-colors hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${compact ? 'h-10 w-10 justify-center' : 'h-9 w-full gap-2.5 px-2.5 text-left'}`}>
    <Plus aria-hidden="true" className="h-4 w-4 shrink-0" />{!compact && <span>New</span>}
  </button>
}
