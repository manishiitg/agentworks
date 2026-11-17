import { openWorkspaceSettings } from './workspaceSettingsNavigation'

let requested = false
export function openSubscriptionLimits() {
  requested = true
  openWorkspaceSettings('providers')
  window.dispatchEvent(new Event('show-subscription-limits'))
}
export function takeSubscriptionLimitsRequest() {
  const next = requested
  requested = false
  return next
}
