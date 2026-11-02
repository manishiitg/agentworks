export type ComputerOS = 'windows' | 'mac' | 'linux' | 'unknown'

export function computerOS(userAgent = typeof navigator === 'undefined' ? '' : navigator.userAgent): ComputerOS {
  if (/android|iphone|ipad|mobile/i.test(userAgent)) return 'unknown'
  if (/windows/i.test(userAgent)) return 'windows'
  if (/macintosh|mac os x/i.test(userAgent)) return 'mac'
  if (/linux/i.test(userAgent) && !/android/i.test(userAgent)) return 'linux'
  return 'unknown'
}

function psQuote(value: string) { return `'${value.replace(/'/g, "''")}'` }
function shellQuote(value: string) { return `'${value.replace(/'/g, "'\\''")}'` }

export function windowsInstallCommand(server: string) {
  const base = server.replace(/\/+$/, '')
  return `$env:AGENTWORKS_SERVER=${psQuote(base)}; irm ${psQuote(`${base}/api/downloads/cli/install-agentworks.ps1`)} | iex`
}

export function unixCliInstallCommand(server: string, noLogin = false) {
  const base = server.replace(/\/+$/, '')
  return `curl -fsSL ${shellQuote(`${base}/api/downloads/cli/install-agentworks.sh`)} | sh -s -- --server ${shellQuote(base)}${noLogin ? ' --no-login' : ''}`
}

export const windowsDefenderNote = 'If Windows Defender blocks AgentWorks: open Windows Security › Virus & threat protection › Protection history, open the agentworks.exe entry, choose Actions › Allow on device, then run this install command again.'
