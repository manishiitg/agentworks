/** Each owner's default chat is saved as "<owner> · Builder"; show it as "Main". Full ownership stays in the tooltip. */
export function compactOwnerChatName(name: string, owner?: string): string {
  return owner && name.trim() === owner + ' · Builder' ? 'Main' : name
}

/** A person's name from their account: "manish.excel2011@gmail.com" gives "manish.excel2011". */
export function ownerShortName(owner: string): string {
  return owner.split('@')[0] || owner
}

/** Two letters that tell another person's tab apart at a glance: "manish.excel2011@gmail.com" gives "ME", and a single name like "Aman" gives "AM". */
export function ownerInitials(owner: string): string {
  const parts = owner.split('@')[0].split(/[^A-Za-z]+/).filter(Boolean)
  if (parts.length === 1) return parts[0].slice(0, 2).toUpperCase()
  return parts.slice(0, 2).map(part => part[0].toUpperCase()).join('')
}
