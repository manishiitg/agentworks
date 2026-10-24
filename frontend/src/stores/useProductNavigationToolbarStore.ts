import { create } from 'zustand'

/** Mounted workspace toolbar slot. DOM-only, never persisted or sent to the server. */
export const useProductNavigationToolbarStore = create<{
  target: HTMLDivElement | null
  targetKind: 'workspace' | 'page' | null
  leadingTarget: HTMLDivElement | null
  workspaceTrigger: HTMLButtonElement | null
  settingsTarget: HTMLDivElement | null
  sidebarSettingsTarget: HTMLDivElement | null
  compactSettingsTarget: HTMLDivElement | null
  sidebarWidth: number
  pickerOpen: boolean
}>(() => ({ target: null, targetKind: null, leadingTarget: null, workspaceTrigger: null, settingsTarget: null, sidebarSettingsTarget: null, compactSettingsTarget: null, sidebarWidth: 0, pickerOpen: false }))
