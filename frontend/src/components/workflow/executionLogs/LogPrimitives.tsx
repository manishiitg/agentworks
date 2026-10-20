import type React from 'react'
import { ChevronRight, CheckCircle, FileText } from 'lucide-react'

import { MarkdownRenderer } from '../../ui/MarkdownRenderer'
import { formatLogFileContent } from './helpers'

export const StepMetadata = ({ description, successCriteria }: { description?: string, successCriteria?: string }) => {
  if (!description && !successCriteria) return null;
  
  return (
    <details className="group border-b border-border bg-muted/10">
      <summary className="flex cursor-pointer list-none items-center gap-2 px-4 py-2 text-xs font-medium text-muted-foreground hover:bg-accent/40 hover:text-foreground">
        <ChevronRight className="h-3.5 w-3.5 transition-transform group-open:rotate-90" />
        <FileText className="h-3.5 w-3.5" />
        Instructions
        {description && <span className="ml-auto text-[10px] font-normal tabular-nums">{description.length.toLocaleString()} chars</span>}
      </summary>
      <div className="max-h-[45vh] space-y-3 overflow-y-auto border-t border-border p-4">
        {description && (
          <div>
            <div className="mb-1 flex items-center gap-1.5 text-xs font-medium text-muted-foreground">
              <FileText className="h-3 w-3" /> Description
            </div>
            <p className="whitespace-pre-wrap text-xs leading-relaxed text-foreground">
              {description}
            </p>
          </div>
        )}
        {successCriteria && (
          <div>
            <div className="mb-1 flex items-center gap-1.5 text-xs font-medium text-muted-foreground">
              <CheckCircle className="h-3 w-3" /> Success Criteria
            </div>
            <p className="rounded border border-emerald-500/15 bg-emerald-500/[0.04] p-2 text-xs leading-relaxed text-foreground">
              {successCriteria}
            </p>
          </div>
        )}
      </div>
    </details>
  )
}

export const StructuredJsonView = ({ value, label = 'Technical details', collapsed = true }: { value: unknown; label?: string; collapsed?: boolean }) => {
  const formattedJson = formatLogFileContent(value)
  const body = (
    <pre className="max-h-[60vh] overflow-auto bg-[#0b0e12] p-3 font-mono text-xs leading-5 text-slate-200 selection:bg-sky-500/35 whitespace-pre">
      <code>{formattedJson}</code>
    </pre>
  )

  if (!collapsed) {
    return (
      <div className="overflow-hidden rounded-md border border-border bg-background/70">
        {body}
      </div>
    )
  }

  return (
    <details className="group/json overflow-hidden rounded-md border border-border bg-background/70">
      <summary className="flex cursor-pointer list-none items-center gap-2 px-3 py-2 text-xs font-semibold text-foreground hover:bg-accent/40">
        <ChevronRight className="h-3.5 w-3.5 text-muted-foreground transition-transform group-open/json:rotate-90" />
        {label}
        <span className="ml-auto text-[10px] font-normal text-muted-foreground">Formatted JSON</span>
      </summary>
      <div className="border-t border-border">{body}</div>
    </details>
  )
}

export const StepMetricChip = ({ title, children }: { title: string; children: React.ReactNode }) => (
  <span
    title={title}
    className="inline-flex items-center gap-1 rounded-md border border-border bg-muted/50 px-1.5 py-0.5 text-[10px] font-medium text-muted-foreground"
  >
    {children}
  </span>
)
