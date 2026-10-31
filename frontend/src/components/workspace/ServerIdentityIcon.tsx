import { Server, Building2, Cloud, Globe, Briefcase, Network, Code2, Rocket } from 'lucide-react'
import { useSidebarExpansionStore } from '../../stores/useSidebarExpansionStore'

export const serverIconOptions = [
  { id: 'server', label: 'Server', Icon: Server },
  { id: 'building', label: 'Building', Icon: Building2 },
  { id: 'cloud', label: 'Cloud', Icon: Cloud },
  { id: 'globe', label: 'Globe', Icon: Globe },
  { id: 'briefcase', label: 'Briefcase', Icon: Briefcase },
  { id: 'network', label: 'Network', Icon: Network },
  { id: 'code', label: 'Code', Icon: Code2 },
  { id: 'rocket', label: 'Rocket', Icon: Rocket },
]

export function ServerIdentityIcon({ connectionId, className, hidden = false }: { connectionId: string; className?: string; hidden?: boolean }) {
  const choice = useSidebarExpansionStore(state => state.connectionIcons[connectionId])
  const option = serverIconOptions.find(item => item.id === choice) || serverIconOptions[0]
  const Icon = option.Icon
  return <Icon className={className} aria-hidden={hidden} aria-label={hidden ? undefined : 'On server'} data-server-icon={option.id} />
}
