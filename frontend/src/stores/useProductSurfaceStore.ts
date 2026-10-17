import { useSidebarHistoryStore } from './useSidebarHistoryStore'
import { useWorkspaceReturnStore } from './useWorkspaceReturnStore'
import { useCommandDialogStore } from './useCommandDialogStore'
import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { isProductSurface, type ProductSurface } from '../products/productSurfaceConfig'

export type { ProductSurface } from '../products/productSurfaceConfig'

interface ProductSurfaceState {
  productSurface: ProductSurface
  lastVideoProjectId: string | null
  selectedWorkProjectId: string | null
  selectedCodeProjectId: string | null
  pendingWorkView: 'schedules' | 'bots' | 'triggers' | null
  setProductSurface: (surface: ProductSurface) => void
  setLastVideoProjectId: (projectId: string | null) => void
  setSelectedWorkProjectId: (projectId: string | null) => void
  setSelectedCodeProjectId: (projectId: string | null) => void
  setPendingWorkView: (view: 'schedules' | 'bots' | 'triggers' | null) => void
}

export const useProductSurfaceStore = create<ProductSurfaceState>()(
  persist(
    (set, get) => ({
      productSurface: 'agentworks',
      lastVideoProjectId: null,
      selectedWorkProjectId: null,
      selectedCodeProjectId: null,
      pendingWorkView: null,
      setProductSurface: (productSurface) => {
        if (get().productSurface !== productSurface) {
          useSidebarHistoryStore.setState(state => ({ generation: state.generation + 1, request: state.request?.surface === productSurface ? state.request : null }))
          useCommandDialogStore.getState().requestProductCreate(null)
          useWorkspaceReturnStore.setState({ restore: null })
        }
        set({ productSurface })
      },
      setLastVideoProjectId: (lastVideoProjectId) => set({ lastVideoProjectId }),
      setSelectedWorkProjectId: (selectedWorkProjectId) => { if (get().selectedWorkProjectId !== selectedWorkProjectId) useSidebarHistoryStore.setState(state => ({ generation: state.generation + 1, request: state.request?.surface === 'work' && state.request.projectId === selectedWorkProjectId ? state.request : null })); set({ selectedWorkProjectId }) },
      setSelectedCodeProjectId: (selectedCodeProjectId) => { if (get().selectedCodeProjectId !== selectedCodeProjectId) useSidebarHistoryStore.setState(state => ({ generation: state.generation + 1, request: state.request?.surface === 'code' && state.request.projectId === selectedCodeProjectId ? state.request : null })); set({ selectedCodeProjectId }) },
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
          selectedCodeProjectId: state?.selectedCodeProjectId ?? null,
          pendingWorkView: null,
        } as ProductSurfaceState
      },
    },
  ),
)
