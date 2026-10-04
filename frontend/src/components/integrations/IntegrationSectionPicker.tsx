import { ChevronRight } from 'lucide-react'
import { Button } from '../ui/Button'

/** The shared parent menu for product integrations. */
export function IntegrationSectionPicker({ options, onSelect }: {
  options: Array<{ value: string; label: string }>; onSelect: (value: string) => void
}) {
  return <div className="space-y-2" aria-label="Integration sections">
    {options.map(option => <Button key={option.value} variant="outline" className="h-10 w-full justify-between text-sm" onClick={() => onSelect(option.value)}>{option.label}<ChevronRight className="h-3.5 w-3.5 text-muted-foreground"/></Button>)}
  </div>
}
