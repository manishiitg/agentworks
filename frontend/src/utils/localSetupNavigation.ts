import { openAppPage } from './appNavigation'

export const OPEN_LOCAL_SETUP_EVENT = 'open-local-setup'
let setupRequested = false

/** Reopen the Mac setup guide from the account menu: it opens over the Projects page. */
export function openLocalSetup() {
  setupRequested = true
  openAppPage('/projects')
  window.dispatchEvent(new Event(OPEN_LOCAL_SETUP_EVENT))
}

/** Read once by the Projects page when it mounts after the request. */
export function takeLocalSetupRequest(): boolean {
  const requested = setupRequested
  setupRequested = false
  return requested
}
