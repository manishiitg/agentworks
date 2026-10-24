/** Short product names for account summaries and setup navigation. */
const names: Record<string, string> = {
  'codex-cli': 'Codex',
  'cursor-cli': 'Cursor',
  'pi-cli': 'Pi',
}

export function codingProviderName(provider: { id: string; display_name: string }): string {
  return names[provider.id] || provider.display_name.replace(/\s+CLI$/i, '')
}
