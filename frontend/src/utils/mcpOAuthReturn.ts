export function desktopConsentReturnPath(value: string | null): string | null {
  if (!value?.startsWith('/desktop/connect?')) return null
  const url = new URL(value, 'https://agentworks.invalid')
  const state = url.searchParams.get('state'), challenge = url.searchParams.get('challenge')
  if (!state || !challenge || !/^[A-Za-z0-9_-]{43}$/.test(state) || !/^[A-Za-z0-9_-]{43}$/.test(challenge)) return null
  try {
    const cb = new URL(url.searchParams.get('callback') || '')
    if (cb.protocol !== 'http:' || cb.hostname !== '127.0.0.1' || !cb.port || cb.pathname !== '/connected' || cb.search || cb.hash || cb.username || cb.password) return null
    return `/desktop/connect?${new URLSearchParams({ state, challenge, callback: cb.href })}`
  } catch { return null }
}

const PREFIX = 'agentworks:mcp-consent:oauth:'

export function mcpConsentReturnPath(value: string | null): string | null {
  if (!value || !value.startsWith('/') || value.startsWith('//') || value.includes('\\')) return null
  const url = new URL(value, 'https://agentworks.invalid')
  if (url.pathname !== '/oauth/consent' && url.pathname !== '/oauth/vault') return null
  const request = url.searchParams.get('request')
  if (!request || !/^mcp_req_[a-f0-9]{64}$/.test(request) || [...url.searchParams.keys()].some(key => key !== 'request')) return null
  return `${url.pathname}?request=${request}`
}

export function cliConsentReturnPath(value: string | null): string | null {
  if (!value || !value.startsWith('/') || value.startsWith('//') || value.includes('\\')) return null
  const url = new URL(value, 'https://agentworks.invalid')
  if (url.pathname !== '/oauth/cli') return null
  const code = url.searchParams.get('code')
  if (!code || !/^cli_verify_[a-f0-9]{64}$/.test(code) || [...url.searchParams.keys()].some(key => key !== 'code')) return null
  return `/oauth/cli?code=${code}`
}

export function connectionConsentReturnPath(value: string | null): string | null {
	if (value && /^\/mcp\/elicitation\/[a-f0-9-]{36}$/.test(value)) return value
  return desktopConsentReturnPath(value) || mcpConsentReturnPath(value) || cliConsentReturnPath(value)
}

export function rememberMcpConsentReturnPath(value: string | null, state: string): void {
  const path = connectionConsentReturnPath(value)
  try {
    if (path) localStorage.setItem(PREFIX + state, path)
  } catch { /* Browser storage may be unavailable. */ }
}

export function consumeMcpConsentReturnPath(state: string): string | null {
  try {
    const key = PREFIX + state
    const path = localStorage.getItem(key)
    localStorage.removeItem(key)
    return connectionConsentReturnPath(path)
  } catch { return null }
}
