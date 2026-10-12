import { useEffect, useLayoutEffect, useRef } from 'react'
import type { ProductSurface } from '../products/productSurfaceConfig'
import { registerProductUrlView, productUrlViewChanged } from '../utils/productUrlNavigation'

/** A mounted surface exposes its actual view state to the shared history adapter. */
export function useProductUrlView(surface: ProductSurface, selection: { id?: string; view?: string; folder?: string }, navigationRevision?: number) {
  const current = useRef(selection)
  current.current = selection
  useLayoutEffect(() => registerProductUrlView(surface, () => current.current), [surface])
  const previous = useRef({ selection, navigationRevision })
  useEffect(() => {
    const old = previous.current
    previous.current = { selection, navigationRevision }
    if (old.selection.id !== selection.id || old.selection.view !== selection.view || old.selection.folder !== selection.folder) {
      const projection = old.selection.id !== selection.id || (navigationRevision !== undefined && old.navigationRevision === navigationRevision)
      productUrlViewChanged(projection)
    }
  }, [selection.id, selection.view, selection.folder, navigationRevision])
}
