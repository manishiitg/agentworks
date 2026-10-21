import { ProductSurfaceIcon } from '../ProductSurfaceIcon'
import { PRODUCT_SURFACE_LABELS, type ProductSurface } from '../../products/productSurfaceConfig'

/** One product-home link and the current location in the existing toolbar. */
export function ProductBreadcrumb({ surface, section, onHome }: { surface: ProductSurface; section?: string; onHome: () => void }) {
  const label = PRODUCT_SURFACE_LABELS[surface]
  return <div className="flex min-w-0 items-center gap-2">
    <h1 className="shrink-0 text-sm font-semibold"><button type="button" onClick={onHome} aria-label={label} title={`Back to ${label}`} className="inline-flex items-center gap-2 rounded-sm hover:text-primary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"><span aria-hidden="true"><ProductSurfaceIcon surface={surface} className="h-4 w-4" /></span>{label}</button></h1>
    {section && <><span aria-hidden="true" className="text-muted-foreground/50">/</span><span className="truncate text-xs text-muted-foreground">{section}</span></>}
  </div>
}
