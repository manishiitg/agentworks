import { useWorkspaceReturnStore } from './useWorkspaceReturnStore'
import { useCommandDialogStore } from './useCommandDialogStore'
import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { isProductSurface, type ProductSurface } from '../products/productSurfaceConfig'

export type { ProductSurface } from '../products/productSurfaceConfig'

interface ProductSurfaceState {
  productSurface: ProductSurface
  selectedWorkProjectId: string | null
  pendingWorkView: 'schedules' | 'bots' | 'triggers' | null
  setProductSurface: (surface: ProductSurface) => void
  setSelectedWorkProjectId: (projectId: string | null) => void
  setPendingWorkView: (view: 'schedules' | 'bots' | 'triggers' | null) => void
}

export const useProductSurfaceStore = create<ProductSurfaceState>()(
  persist(
    (set, get) => ({
      productSurface: 'agentworks',
      selectedWorkProjectId: null,
      pendingWorkView: null,
      setProductSurface: (productSurface) => {
        if (get().productSurface !== productSurface) {
          useCommandDialogStore.getState().requestProductCreate(null)
          useWorkspaceReturnStore.setState({ restore: null })
        }
        set({ productSurface })
      },
      setSelectedWorkProjectId: (selectedWorkProjectId) => set({ selectedWorkProjectId }),
      setPendingWorkView: (pendingWorkView) => set({ pendingWorkView }),
    }),
    {
      name: 'agentworks-product-surface',
      version: 6,
      migrate: (persisted) => {
        const state = persisted as Partial<ProductSurfaceState> | undefined
        const surface = state?.productSurface
        return {
          ...state,
          productSurface: isProductSurface(surface) ? surface : 'agentworks',
          selectedWorkProjectId: state?.selectedWorkProjectId ?? null,
          pendingWorkView: null,
        } as ProductSurfaceState
      },
    },
  ),
)
