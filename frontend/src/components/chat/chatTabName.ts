/** Keep the main chat label short; full ownership stays in the tab tooltip. */
export function compactOwnerChatName(name: string, owner?: string): string {
  return owner && name.trim() === owner + ' · Builder' ? 'Builder' : name
}

/** Two letters that tell another person's tab apart at a glance: "manish.excel2011@gmail.com" gives "ME", and a single name like "Aman" gives "AM". */
export function ownerInitials(owner: string): string {
  const parts = owner.split('@')[0].split(/[^A-Za-z]+/).filter(Boolean)
  if (parts.length === 1) return parts[0].slice(0, 2).toUpperCase()
  return parts.slice(0, 2).map(part => part[0].toUpperCase()).join('')
}
