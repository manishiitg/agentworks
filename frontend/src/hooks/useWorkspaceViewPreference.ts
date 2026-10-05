import { useCallback, useLayoutEffect, useState } from 'react'
import type { ProductSurface } from '../products/productSurfaceConfig'
import { useWorkspaceConnectionStore } from '../stores/useWorkspaceConnectionStore'
import { readWorkspaceViewPreference, workspaceViewPreferenceKey, writeWorkspaceViewPreference } from '../utils/workspaceViewPreference'

/** Shared by product toolbars; defaults and asynchronous loading never persist. */
export function useWorkspaceViewPreference<T extends string>(
  product: ProductSurface, entity: string, defaultView: T,
  normalize: (value: unknown) => T | null,
): readonly [T, (view: T) => void] {
  const workspace = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  const key = workspaceViewPreferenceKey(product, entity)
  const load = () => readWorkspaceViewPreference(product, entity, normalize) ?? defaultView
  const [selection, setSelection] = useState(() => ({ key, view: load() }))
  const view = selection.key === key ? selection.view : load()
  useLayoutEffect(() => {
    setSelection(current => current.key === key ? current : { key, view })
  }, [key, view, workspace])
  const select = useCallback((next: T) => {
    writeWorkspaceViewPreference(product, entity, next)
    setSelection({ key, view: next })
  }, [product, entity, key])
  return [view, select]
}
