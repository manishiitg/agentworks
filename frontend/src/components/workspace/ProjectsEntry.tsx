import { useEffect } from 'react'
import { useCommandDialogStore } from '../../stores/useCommandDialogStore'
import { openAppPage } from '../../utils/appNavigation'
import { currentProductRoute } from '../../utils/productRoutes'

/** A product with no project open shows Projects; explicit create requests keep their existing form. */
export function ProjectsEntry() {
  useEffect(() => {
    const pending = useCommandDialogStore.getState()
    if (!currentProductRoute()?.id && !pending.productCreateSurface && !pending.showPresetCreate) openAppPage('/projects')
  }, [])
  return <div className="grid h-full place-items-center"><button type="button" onClick={() => openAppPage('/projects')} className="rounded-lg px-4 py-2 text-sm text-primary hover:bg-muted">Open Projects</button></div>
}
