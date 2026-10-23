import { EntityIdentityIcon } from '../ui/EntityIdentityIcon'

export function chatOwnerInitials(name: string): string {
  const words = name.split('@')[0].split(/[\s._-]+/).filter(Boolean)
  return words.slice(0, 2).map(word => Array.from(word)[0]).join('').toLocaleUpperCase() || '?'
}

/** Compact identity only; the server's can_write still controls every action. */
export function ChatTabOwner({ name, yours }: { name: string; yours: boolean }) {
  if (yours) return null
  const label = `Tab owned by ${name}`
  return <span role="img" aria-label={label} title={label} className="inline-flex shrink-0 items-center">
    <EntityIdentityIcon icon={chatOwnerInitials(name)} label={name} className="rounded-full" />
  </span>
}

export function compactOwnerChatName(name: string, owner?: string): string {
  return owner && name.trim() === `${owner} · Builder` ? 'Builder' : name
}
