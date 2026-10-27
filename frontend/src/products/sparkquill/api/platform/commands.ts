import type { QuickCommand } from '../../stores/types'

/** Keep the manifest's command identity separate from its display label. */
export function quickCommandsFromProfile(profile: { commands?: Array<Record<string, unknown>> } | null | undefined): QuickCommand[] {
  return (profile?.commands ?? []).flatMap((c) => {
    const name = typeof c.name === 'string' ? c.name.trim() : ''
    const label = typeof c.description === 'string' && c.description.trim() ? c.description.trim() : name
    const message = typeof c.prompt === 'string' ? c.prompt.trim() : ''
    if (!name || !message) return []
    const aliases = Array.isArray(c.aliases)
      ? c.aliases.filter((alias): alias is string => typeof alias === 'string' && !!alias.trim()).map(alias => alias.trim())
      : []
    return [{ name, label, message, ...(aliases.length ? { aliases } : {}), ...(c.menu_hidden === true ? { menuHidden: true } : {}) }]
  })
}
