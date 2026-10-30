import type { DesktopConnection } from '../../services/desktopConnections'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '../ui/select'

/** Compact, keyboard-accessible location menu; long names never size the sidebar. */
export function LocationFilter({ label, value, onChange, connections }: {
  label: string; value: string; onChange: (value: string) => void; connections: DesktopConnection[]
}) {
  return <Select value={value} onValueChange={onChange}>
    <SelectTrigger aria-label={label} className="h-7 w-auto max-w-28 gap-1.5 border-0 px-2 py-1 text-[11px] text-muted-foreground shadow-none hover:bg-muted [&>svg]:h-3 [&>svg]:w-3">
      <SelectValue />
    </SelectTrigger>
    <SelectContent data-location-filter-menu="" align="end" sideOffset={4} className="w-40 max-w-48 rounded-lg p-1 shadow-lg">
      <SelectItem value="all" className="text-xs">All</SelectItem>
      {connections.map(connection => <SelectItem key={connection.id} value={connection.id} className="text-xs"><span className="block max-w-28 truncate" title={connection.server || connection.name}>{connection.name}</span></SelectItem>)}
    </SelectContent>
  </Select>
}
