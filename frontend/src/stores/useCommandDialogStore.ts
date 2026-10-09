import { create } from 'zustand'

export type ProductCreateSurface = 'agentworks' | 'relays' | 'work' | 'code' | 'video-studio'

export type DialogName = 'skillImport' | 'mcpDetails' | 'mcpConfig' | 'models' | 'presetSettings' | 'presetCreate'

interface CommandDialogState {
  /** Retained until the destination product mounts and opens its existing form. */
  productCreateSurface: ProductCreateSurface | null
  requestProductCreate: (surface: ProductCreateSurface | null) => void
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
  requestProductCreate: surface => set({ productCreateSurface: surface }),
  consumeProductCreate: surface => {
    if (get().productCreateSurface !== surface) return false
    set({ productCreateSurface: null })
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
    showSkillImport: false,
    showMCPDetails: false,
    showMCPConfig: false,
    showModels: false,
    showPresetCreate: false,
    showPresetSettings: false,
  }),
}))
