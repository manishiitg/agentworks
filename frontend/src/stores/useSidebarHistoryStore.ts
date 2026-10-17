import { create } from 'zustand'
import type { ProductSurface } from '../products/productSurfaceConfig'
import type { ChatHistorySession } from '../services/api-types'
export type SidebarHistoryRequest = {
  surface: ProductSurface
  projectId?: string
  session: ChatHistorySession
  identity: number
}
/** Ephemeral navigation handoff; never persisted across account/server changes. */
export const useSidebarHistoryStore = create<{ request: SidebarHistoryRequest | null; generation: number }>(
  () => ({ request: null, generation: 0 })
)
