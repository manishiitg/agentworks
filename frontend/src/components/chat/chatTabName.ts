/** Keep the main chat label short; full ownership stays in the tab tooltip. */
export function compactOwnerChatName(name: string, owner?: string): string {
  return owner && name.trim() === owner + ' · Builder' ? 'Builder' : name
}
