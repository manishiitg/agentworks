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
  'text-sky-600 dark:text-sky-400',
  'text-violet-600 dark:text-violet-400',
  'text-emerald-600 dark:text-emerald-400',
  'text-amber-600 dark:text-amber-400',
  'text-rose-600 dark:text-rose-400',
  'text-cyan-600 dark:text-cyan-400',
  'text-fuchsia-600 dark:text-fuchsia-400',
  'text-orange-600 dark:text-orange-400',
]

/** Use server identity, not its editable name or position in the filtered list. */
export function serverIconColors(connections: DesktopConnection[]): Record<string, string> {
  const colors: Record<string, string> = {}
  const used = new Set<number>()
  for (const server of [...new Set(connections.map(connection => connection.server).filter(Boolean))].sort()) {
    let hash = 0
    for (const char of server) hash = (Math.imul(hash, 31) + char.charCodeAt(0)) >>> 0
    let index = hash % serverIconPalette.length
    for (let attempt = 0; attempt < serverIconPalette.length && used.has(index); attempt++) index = (index + 1) % serverIconPalette.length
    used.add(index)
    colors[server] = serverIconPalette[index]
  }
  return colors
}
