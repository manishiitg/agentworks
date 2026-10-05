import { ChevronRight } from 'lucide-react'
import { Button } from '../ui/Button'
import type { IntegrationSectionOption } from './integrationSections'

/** The shared parent menu for product integrations. */
export function IntegrationSectionPicker({ options, onSelect }: {
  options: IntegrationSectionOption[]; onSelect: (value: string) => void
}) {
  return <div className="space-y-2" aria-label="Integration sections">
    {options.map(({ value, label, description, icon: Icon }) => <Button key={value} variant="outline"
      className="h-auto min-h-14 w-full justify-between gap-3 px-3 py-3 text-left whitespace-normal" onClick={() => onSelect(value)}>
      <span className="flex min-w-0 items-center gap-3">
        <Icon className="h-4 w-4 shrink-0 text-muted-foreground" aria-hidden />
        <span className="min-w-0">
          <span className="block text-sm font-medium text-foreground">{label}</span>
          <span className="mt-0.5 block text-xs font-normal text-muted-foreground">{description}</span>
        </span>
      </span>
      <ChevronRight className="h-3.5 w-3.5 shrink-0 text-muted-foreground" aria-hidden />
    </Button>)}
  </div>
}
