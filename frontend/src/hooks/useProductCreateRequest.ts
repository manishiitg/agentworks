import { useEffect, useRef } from 'react'
import { useCommandDialogStore, type ProductCreateSurface } from '../stores/useCommandDialogStore'
import { useProductSurfaceStore } from '../stores/useProductSurfaceStore'

/** Consume a creation request once, even when the product loads lazily. */
export function useProductCreateRequest(surface: ProductCreateSurface | null, open: () => void) {
  const request = useCommandDialogStore(state => state.productCreateSurface)
  const current = useProductSurfaceStore(state => state.productSurface)
  const openRef = useRef(open)
  openRef.current = open
  useEffect(() => {
    if (!surface || request !== surface || current !== surface) return
    if (useCommandDialogStore.getState().consumeProductCreate(surface)) openRef.current()
  }, [surface, request, current])
}
