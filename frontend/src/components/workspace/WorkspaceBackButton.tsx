import { ArrowLeft } from 'lucide-react'
import { useProductSurfaceStore } from '../../stores/useProductSurfaceStore'
import { PRODUCT_SURFACE_LABELS } from '../../products/productSurfaceConfig'
import { openProductWorkspace } from '../../utils/productWorkspaceNavigation'

/** A labeled return action belongs to the page, rather than the icon strip. */
export function WorkspaceBackButton({ onBack }: { onBack?: () => void } = {}) {
  const surface = useProductSurfaceStore(state => state.productSurface)
  return <button type="button" onClick={() => { onBack?.(); openProductWorkspace(surface) }}
    className="inline-flex shrink-0 items-center gap-1.5 rounded-md px-2 py-1.5 text-xs font-medium text-muted-foreground transition-colors hover:bg-muted hover:text-foreground">
    <ArrowLeft className="h-4 w-4" aria-hidden="true" />
    Back to {PRODUCT_SURFACE_LABELS[surface]}
  </button>
}
