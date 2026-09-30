import type { McpCatalogServer } from '../../api/mcpCatalog'

// Catalog servers that share one sign-in (Google's services): shown as one
// card and signed in once (docs/design/personal_mcp_attach.md).

/** Display name of a sign-in group of several catalog servers. */
export const providerGroupLabel = (group: string) => group === 'google' ? 'Google Workspace' : group.charAt(0).toUpperCase() + group.slice(1)

/** A service's short name inside its group card: GoogleGmail → Gmail. */
export const groupServiceLabel = (catalog: string, group: string) => {
  const prefix = providerGroupLabel(group).split(' ')[0]
  return catalog.toLowerCase().startsWith(prefix.toLowerCase()) && catalog.length > prefix.length ? catalog.slice(prefix.length) : catalog
}

/** Sign-in groups with more than one catalog server (Google's 8), by key. */
export function providerGroups(catalog: McpCatalogServer[]): Map<string, McpCatalogServer[]> {
  const groups = new Map<string, McpCatalogServer[]>()
  for (const entry of catalog) {
    if (!entry.group) continue
    groups.set(entry.group, [...(groups.get(entry.group) ?? []), entry])
  }
  for (const [key, entries] of groups) if (entries.length < 2) groups.delete(key)
  return groups
}

