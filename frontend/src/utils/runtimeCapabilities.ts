type RuntimeCapabilityWindow = Window & {
  __APP_RUNTIME_CONFIG__?: {
    cdpEnabled?: boolean | string
  }
}

// The backend bootstrap advertises installation capabilities.
export function isBrowserCDPEnabled(): boolean {
  if (typeof window === 'undefined') return false
  const value = (window as RuntimeCapabilityWindow).__APP_RUNTIME_CONFIG__?.cdpEnabled
  return value === true || value === 'true'
}
