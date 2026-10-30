import type { DesktopConnection } from '../services/desktopConnections'

/** Friendly saved names win; old hostname labels display only the domain name. */
export function connectionDisplayName(connection: DesktopConnection): string {
  if (connection.id === 'local') return 'Local'
  const name = connection.name.trim()
  try {
    const host = new URL(connection.server).hostname
    if (name && name.toLowerCase() !== host.toLowerCase() && name !== connection.server) return name
    if (/^\d+(?:\.\d+){3}$/.test(host) || host.includes(':')) return 'Server'
    const parts = host.split('.')
    const index = parts.length > 2 && /^(co|com|org|net|ac|gov)$/.test(parts.at(-2)!) ? -3 : -2
    const word = parts.at(index) || host
    return word.charAt(0).toUpperCase() + word.slice(1)
  } catch { return name || 'Server' }
}

export const connectedProjectKey = (connection: string, kind: string, id: string) => JSON.stringify([connection, kind, id])
