import { ProductSurfaceIcon } from '../ProductSurfaceIcon'
import type { ProductSurface } from '../../products/productSurfaceConfig'

/** Project identity stays visible while people switch between its chats. A plain label (owner, 4 Nov): switching
 * Crews is done from the sidebar, not from here. */
export function ProjectWorkspaceTitle({ name, surface }: { name: string; surface: ProductSurface }) {
  return <span data-project-title title={name}
    className="mr-0 md:mr-2 inline-flex h-10 min-w-0 max-w-[50vw] md:h-8 md:shrink-0 items-center gap-1 md:gap-2 px-1 text-sm font-semibold md:max-w-56">
    <ProductSurfaceIcon surface={surface} className="h-4 w-4 shrink-0" />
    <span className="truncate">{name}</span>
  </span>
}
