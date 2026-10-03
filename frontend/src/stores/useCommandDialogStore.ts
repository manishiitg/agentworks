import { create } from 'zustand'

export type DialogName = 'skillImport' | 'mcpDetails' | 'mcpConfig' | 'models' | 'presetSettings' | 'presetCreate'

interface CommandDialogState {
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

export const useCommandDialogStore = create<CommandDialogState>()((set) => ({
  showSkillImport: false,
  showMCPDetails: false,
  showMCPConfig: false,
  showModels: false,
  showPresetCreate: false,
  showPresetSettings: false,
  openDialog: (dialog) => set({ [dialogKeyMap[dialog]]: true }),
  closeDialog: (dialog) => set({ [dialogKeyMap[dialog]]: false }),
  closeAll: () => set({
    showSkillImport: false,
    showMCPDetails: false,
    showMCPConfig: false,
    showModels: false,
    showPresetCreate: false,
    showPresetSettings: false,
  }),
}))
