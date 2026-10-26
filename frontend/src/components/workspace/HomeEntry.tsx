import { useEffect } from 'react'
import { useCommandDialogStore } from '../../stores/useCommandDialogStore'
import { openHome } from '../../utils/homeNavigation'
import { currentProductRoute } from '../../utils/productRoutes'

/** Empty products use Home; explicit create requests keep their existing form. */
export function HomeEntry() {
  useEffect(() => {
    const pending = useCommandDialogStore.getState()
    if (!currentProductRoute()?.id && !pending.productCreateSurface && !pending.showPresetCreate) openHome()
  }, [])
  return <div className="grid h-full place-items-center"><button type="button" onClick={() => openHome()} className="rounded-lg px-4 py-2 text-sm text-primary hover:bg-muted">Open Home</button></div>
}
