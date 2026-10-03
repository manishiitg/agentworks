import type { ReactNode } from 'react'
import { ChevronDown } from 'lucide-react'
import { McpToolArguments } from './McpToolArguments'

/** One tool layout for connected MCPs and group permissions. */
export function McpToolCard({ name, description, status, schema, rawSchema, selection, children }: {
  name: string; description?: string; status?: string; schema?: string | null;
  rawSchema?: Record<string, unknown>; selection?: ReactNode; children?: ReactNode
}) {
  return <div className="min-w-0 rounded-md border border-border/60 p-3" data-tool-card={name}>
    <div className="flex items-start gap-2">
      {selection}
      <div className="min-w-0 flex-1">
        <p className="break-all font-medium text-foreground">{name}</p>
        {status && <p className="text-xs text-muted-foreground">{status}</p>}
        {description && <p className="mt-1 truncate text-muted-foreground" title={description}>{description.split('\n')[0]}</p>}
      </div>
    </div>
    <details className="group/arguments mt-2">
      <summary aria-label={`Arguments for ${name}`} className="flex w-fit cursor-pointer list-none items-center gap-1 text-xs text-muted-foreground hover:text-foreground [&::-webkit-details-marker]:hidden">
        <ChevronDown className="h-3 w-3 -rotate-90 transition-transform group-open/arguments:rotate-0" aria-hidden />Arguments
      </summary>
      <div className="mt-2 w-full" aria-label={`${name} arguments`}><McpToolArguments schema={schema} rawSchema={rawSchema} /></div>
    </details>
    {children && <div className="mt-3 space-y-2">{children}</div>}
  </div>
}
