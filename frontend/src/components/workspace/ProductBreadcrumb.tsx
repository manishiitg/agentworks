import { useCallback, useRef } from 'react'
import { ChevronDown } from 'lucide-react'
import { useProductNavigationToolbarStore } from '../../stores/useProductNavigationToolbarStore'
import { ProductMenuItems, type ProductMenuItem } from './ProductMenuItems'
import { ProductSurfaceIcon } from '../ProductSurfaceIcon'
import { ProductSurfaceSwitcher } from '../ProductSurfaceSwitcher'
import { PRODUCT_SURFACE_LABELS, type ProductSurface } from '../../products/productSurfaceConfig'

/** One product-home link and the current location in the existing toolbar. */
export function ProductBreadcrumb({ surface, section, onHome, menuItems, projectPickerActive = true, standalone = false }: { surface: ProductSurface; section?: string; onHome: () => void; menuItems?: readonly ProductMenuItem[]; projectPickerActive?: boolean; standalone?: boolean }) {
  const projectPicker = surface === 'agentworks' || surface === 'relays'
  const ownTrigger = useRef<HTMLButtonElement | null>(null)
  const register = useCallback((node: HTMLButtonElement | null) => {
    const previous = ownTrigger.current
    ownTrigger.current = node
    if (!projectPicker) return
    const state = useProductNavigationToolbarStore.getState()
    if (node && projectPickerActive) useProductNavigationToolbarStore.setState({ workspaceTrigger: node })
    else if (state.workspaceTrigger === previous) useProductNavigationToolbarStore.setState({ workspaceTrigger: null })
  }, [projectPicker, projectPickerActive])
  const pickerOpen = useProductNavigationToolbarStore(state => state.pickerOpen)
  const label = PRODUCT_SURFACE_LABELS[surface]
  return <div className="flex min-w-0 items-center gap-2">
    <h1 className="shrink-0 text-sm font-semibold"><button type="button" onClick={onHome} aria-label={label} title={`Back to ${label}`} className="inline-flex items-center gap-2 rounded-sm hover:text-primary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"><span aria-hidden="true"><ProductSurfaceIcon surface={surface} className="h-4 w-4" /></span>{label}</button></h1>
    {projectPicker ? <button ref={register} type="button" data-product-header-picker aria-label="Switch workspace or product" aria-haspopup="menu" aria-expanded={pickerOpen} title="Switch workspace or product"
      onClick={() => window.dispatchEvent(new Event(pickerOpen ? 'close-workspace-picker' : 'open-workspace-picker'))}
      onKeyDown={event => { if (event.key === 'ArrowDown') { event.preventDefault(); window.dispatchEvent(new CustomEvent('open-workspace-picker', { detail: { focusFirst: true } })) } }} className="inline-flex h-7 w-6 shrink-0 items-center justify-center rounded-md text-muted-foreground hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"><ChevronDown aria-hidden="true" className={`h-3 w-3 ${pickerOpen ? 'rotate-180' : ''}`} /></button>
      : <ProductSurfaceSwitcher standalone={standalone} variant="chevron" menuTitle={label} menuContent={menuItems ? close => <ProductMenuItems items={menuItems} onSelect={close} /> : undefined} />}
    {section && <><span aria-hidden="true" className="text-muted-foreground/50">/</span><span className="truncate text-xs text-muted-foreground">{section}</span></>}
  </div>
}
