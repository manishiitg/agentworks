import { Loader2, Users } from 'lucide-react'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '../ui/tooltip'

export type SharedPerson = { name: string; state: 'working' | 'waiting' | 'online' | 'offline'; line?: string }

/** A shared project's people icon: a dot when anyone else is online, and on hover (the app's own tooltip) who shared it
 * and each person's state, as the server sends them (owner, 10 Nov). */
export function SharedPeopleIcon({ sharedLine, people }: { sharedLine?: string; people: SharedPerson[] }) {
  const anyoneOn = people.some(person => person.state !== 'offline')
  const label = [sharedLine, ...people.map(person => `${person.name} · ${person.state}${person.line ? ` · ${person.line}` : ''}`)].filter(Boolean).join(', ')
  return <TooltipProvider delayDuration={150}>
    <Tooltip>
      <TooltipTrigger asChild>
        <span role="img" aria-label={label} className="relative ml-1.5 inline-flex align-[-1px] text-muted-foreground">
          <Users aria-hidden="true" className="h-3 w-3" />
          {people.length > 0 && <span aria-hidden="true" className={`absolute -bottom-0.5 -right-0.5 h-1.5 w-1.5 rounded-full ring-1 ring-background ${anyoneOn ? 'bg-emerald-500' : 'bg-muted-foreground/50'}`} />}
        </span>
      </TooltipTrigger>
      <TooltipContent side="right" className="w-60 px-2 py-2 text-xs">
        {sharedLine && <p className="mb-1 truncate text-muted-foreground">{sharedLine}</p>}
        {people.map(person => <div key={person.name} className="py-0.5">
          <div className="flex items-center gap-2">
            <span aria-hidden="true" className={`h-1.5 w-1.5 shrink-0 rounded-full ${person.state === 'offline' ? 'bg-muted-foreground/50' : 'bg-emerald-500'}`} />
            <span className="min-w-0 flex-1 truncate">{person.name}</span>
            {(person.state === 'working' || person.state === 'waiting') && <span className="shrink-0 text-muted-foreground">{person.state}</span>}
            {person.state === 'working' && <Loader2 aria-hidden="true" className="h-3 w-3 shrink-0 animate-spin text-[hsl(var(--info))]" />}
          </div>
          {person.line && <p className="truncate pl-3.5 text-[11px] text-muted-foreground">{person.line}</p>}
        </div>)}
        {!people.length && !sharedLine && <p className="text-muted-foreground">Shared with you</p>}
      </TooltipContent>
    </Tooltip>
  </TooltipProvider>
}
