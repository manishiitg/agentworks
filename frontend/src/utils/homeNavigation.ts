import { openAppPage } from './appNavigation'

export const OPEN_HOME_EVENT = 'open-welcome-home'

export function openHome(focusPrompt = false) {
  window.dispatchEvent(new Event('close-workspace-picker'))
  openAppPage('/home')
  window.dispatchEvent(new CustomEvent(OPEN_HOME_EVENT, { detail: { focusPrompt } }))
}

/** Reopen onboarding from the local Mac account menu without resetting anything. */
export function openLocalSetup() {
  openHome()
  window.dispatchEvent(new CustomEvent(OPEN_HOME_EVENT, { detail: { setup: true } }))
}
