import { useEffect, useRef } from 'react'
import { useCommandDialogStore, type ProductCreateSurface, type ProductCreateIntent } from '../stores/useCommandDialogStore'
import { useProductSurfaceStore } from '../stores/useProductSurfaceStore'

/** Consume a creation request once, even when the product loads lazily. */
export function useProductCreateRequest(surface: ProductCreateSurface | null, open: (intent?: ProductCreateIntent) => void) {
  const request = useCommandDialogStore(state => state.productCreateSurface)
  const current = useProductSurfaceStore(state => state.productSurface)
  const openRef = useRef(open)
  openRef.current = open
  useEffect(() => {
    if (!surface || request !== surface || current !== surface) return
    const store = useCommandDialogStore.getState()
    const intent = store.productCreateIntent
    if (store.consumeProductCreate(surface)) openRef.current(intent ?? undefined)
  }, [surface, request, current])
}
