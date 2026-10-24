import { create } from 'zustand'

/** Ephemeral shell slots and page actions; never persisted or sent to the server. */
export const useProductNavigationToolbarStore = create<{
  target: HTMLDivElement | null
  targetKind: 'workspace' | 'page' | null
  leadingTarget: HTMLDivElement | null
  workspaceTrigger: HTMLButtonElement | null
  settingsTarget: HTMLDivElement | null
  sidebarSettingsTarget: HTMLDivElement | null
  compactSettingsTarget: HTMLDivElement | null
  setupActions: { owner: object; onOpenWalkthrough?: () => void; onOpenShortcuts?: () => void } | null
  sidebarWidth: number
  pickerOpen: boolean
}>(() => ({ target: null, targetKind: null, leadingTarget: null, workspaceTrigger: null, settingsTarget: null, sidebarSettingsTarget: null, compactSettingsTarget: null, setupActions: null, sidebarWidth: 0, pickerOpen: false }))
