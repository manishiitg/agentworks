import { create } from 'zustand'
import type { CrewStarterType } from '../products/work/crewStarterTypes'
export type ProductCreateIntent = 'studio' | CrewStarterType

export type ProductCreateSurface = 'agentworks' | 'relays' | 'work'

export type DialogName = 'skillImport' | 'mcpDetails' | 'mcpConfig' | 'models' | 'presetSettings' | 'presetCreate'

interface CommandDialogState {
  /** Retained until the destination product mounts and opens its existing form. */
  productCreateSurface: ProductCreateSurface | null
  productCreateIntent: ProductCreateIntent | null
  requestProductCreate: (surface: ProductCreateSurface | null, intent?: ProductCreateIntent) => void
  consumeProductCreate: (surface: ProductCreateSurface) => boolean
  showSkillImport: boolean
  showMCPDetails: boolean
  showMCPConfig: boolean
  showModels: boolean
  showPresetCreate: boolean
  showPresetSettings: boolean
  openDialog: (dialog: DialogName) => void
  closeDialog: (dialog: DialogName) => void
  closeAll: () => void
}

const dialogKeyMap: Record<DialogName, keyof CommandDialogState> = {
  skillImport: 'showSkillImport',
  mcpDetails: 'showMCPDetails',
  mcpConfig: 'showMCPConfig',
  models: 'showModels',
  presetSettings: 'showPresetSettings',
  presetCreate: 'showPresetCreate',
}

export const useCommandDialogStore = create<CommandDialogState>()((set, get) => ({
  productCreateSurface: null,
  productCreateIntent: null,
  requestProductCreate: (surface, intent) => set({ productCreateSurface: surface, productCreateIntent: surface ? intent ?? null : null }),
  consumeProductCreate: surface => {
    if (get().productCreateSurface !== surface) return false
    set({ productCreateSurface: null, productCreateIntent: null })
    return true
  },
  showSkillImport: false,
  showMCPDetails: false,
  showMCPConfig: false,
  showModels: false,
  showPresetCreate: false,
  showPresetSettings: false,
  openDialog: (dialog) => set({ [dialogKeyMap[dialog]]: true }),
  closeDialog: (dialog) => set({ [dialogKeyMap[dialog]]: false }),
  closeAll: () => set({
    productCreateSurface: null,
    productCreateIntent: null,
    showSkillImport: false,
    showMCPDetails: false,
    showMCPConfig: false,
    showModels: false,
    showPresetCreate: false,
    showPresetSettings: false,
  }),
}))
