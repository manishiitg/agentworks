import { Terminal, type LucideIcon } from 'lucide-react'
import { DynamicIcon, iconNames, type IconName } from 'lucide-react/dynamic'

const lucideNames = new Set<string>(iconNames)

// CommandIcon draws a command's icon by its Lucide name. Each icon loads on
// demand, so the bundle does not carry the whole library. The fallback shows
// while the icon loads and for an empty or unknown name.
export function CommandIcon({ name, fallback: Fallback = Terminal, className }: { name?: string; fallback?: LucideIcon; className?: string }) {
  const fallback = () => <Fallback className={className} />
  if (!name || !lucideNames.has(name)) return fallback()
  return <DynamicIcon name={name as IconName} className={className} fallback={fallback} />
}
