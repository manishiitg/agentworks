import { useEffect, useLayoutEffect, useRef } from 'react'
import type { ProductSurface } from '../products/productSurfaceConfig'
import { registerProductUrlView, productUrlViewChanged } from '../utils/productUrlNavigation'

/** A mounted surface exposes its actual view state to the shared history adapter. */
export function useProductUrlView(surface: ProductSurface, selection: { id?: string; view?: string; folder?: string }) {
  const current = useRef(selection)
  current.current = selection
  useLayoutEffect(() => registerProductUrlView(surface, () => current.current), [surface])
  const previous = useRef(selection)
  useEffect(() => {
    const old = previous.current
    previous.current = selection
    if (old.id !== selection.id || old.view !== selection.view || old.folder !== selection.folder) productUrlViewChanged()
  }, [selection.id, selection.view, selection.folder])
}
