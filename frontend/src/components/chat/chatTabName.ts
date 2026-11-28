/** Each owner's default agent is saved as "<owner> · Builder"; show it as "Agent" until it is named. Full ownership
 * stays in the tooltip. */
export function compactOwnerChatName(name: string, owner?: string): string {
  return owner && name.trim() === owner + ' · Builder' ? 'Agent' : name
}

/** A person's name from their account: "manish.excel2011@gmail.com" gives "manish.excel2011". */
export function ownerShortName(owner: string): string {
  return owner.split('@')[0] || owner
}

