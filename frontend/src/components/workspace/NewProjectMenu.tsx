import { Plus } from 'lucide-react'
import { useAuthStore } from '../../stores/useAuthStore'
import { useProductSurfaceStore } from '../../stores/useProductSurfaceStore'
import { openQuickNavigation, quickNavigationItems } from '../../utils/quickNavigation'

/** Open the existing project form through the shared, permission-checked action. */
export function NewProjectMenu() {
  const user = useAuthStore(state => state.user)
  const multiUser = useAuthStore(state => state.isMultiUserMode)
  const product = useProductSurfaceStore(state => state.productSurface)
  const createItem = quickNavigationItems(user, product, multiUser).find(item => item.action === 'create' && item.surface === 'work')
  if (!createItem) return null
  return <button type="button" aria-label="New project" title="New" onClick={() => openQuickNavigation(createItem)} className="flex h-9 w-full items-center gap-2.5 rounded-lg px-2.5 text-left text-[13px] text-foreground/85 transition-colors hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
    <Plus aria-hidden="true" className="h-4 w-4 shrink-0" /><span>New</span>
  </button>
}
