import { openAppPage } from './appNavigation'

export const OPEN_HOME_EVENT = 'open-welcome-home'

export function openHome(focusPrompt = false) {
  window.dispatchEvent(new Event('close-workspace-picker'))
  openAppPage('/home')
  window.dispatchEvent(new CustomEvent(OPEN_HOME_EVENT, { detail: { focusPrompt } }))
}
