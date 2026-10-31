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

const serverIconPalette = [
  'text-slate-500/75 dark:text-slate-400/75',
  'text-violet-500/65 dark:text-violet-300/65',
  'text-sky-500/65 dark:text-sky-300/65',
  'text-emerald-500/65 dark:text-emerald-300/65',
  'text-amber-500/65 dark:text-amber-300/65',
  'text-rose-500/65 dark:text-rose-300/65',
  'text-cyan-500/65 dark:text-cyan-300/65',
  'text-orange-500/65 dark:text-orange-300/65',
]

/** Stable server order keeps the first marker neutral and other markers softly colored. */
export function serverIconColors(connections: DesktopConnection[]): Record<string, string> {
  const servers = [...new Set(connections.map(connection => connection.server).filter(Boolean))].sort()
  return Object.fromEntries(servers.map((server, index) => [server, serverIconPalette[index % serverIconPalette.length]]))
}
