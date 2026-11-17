import { openWorkspaceSettings } from './workspaceSettingsNavigation'

let requested = false
let requestedPlace = ''
/** Opens Accounts and usage; place picks one server (a connection id) on the Mac. */
export function openSubscriptionLimits(place = '') {
  requested = true
  requestedPlace = place
  openWorkspaceSettings('providers')
  window.dispatchEvent(new Event('show-subscription-limits'))
}
export function takeSubscriptionLimitsRequest() {
  const next = requested
  requested = false
  return next
}
export function takeSubscriptionLimitsPlace() {
  const next = requestedPlace
  requestedPlace = ''
  return next
}
