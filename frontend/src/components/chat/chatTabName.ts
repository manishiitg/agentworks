/** Keep the main chat label short; full ownership stays in the tab tooltip. */
export function compactOwnerChatName(name: string, owner?: string): string {
  return owner && name.trim() === owner + ' · Builder' ? 'Builder' : name
}

/** Two letters that tell another person's tab apart at a glance: "manish.excel2011@gmail.com" gives "ME". */
export function ownerInitials(owner: string): string {
  const parts = owner.split('@')[0].split(/[^A-Za-z]+/).filter(Boolean)
  return parts.slice(0, 2).map(part => part[0].toUpperCase()).join('')
}
