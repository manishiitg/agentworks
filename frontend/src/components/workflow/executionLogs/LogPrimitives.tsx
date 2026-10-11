import type React from 'react'
import {
  ChevronRight,
  CheckCircle,
  XCircle,
  FileText,
  Loader2,
  Gauge,
  Terminal,
  Bot,
  User,
} from 'lucide-react'
import type { PulseReviewRunLog } from '../../../services/api-types'
import { formatStartedAt } from '../../../utils/duration'
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

export const PulseReviewsPanel = ({ reviews }: { reviews: PulseReviewRunLog[] }) => {
  if (reviews.length === 0) return null

  return (
    <section className="overflow-hidden rounded-lg border border-border bg-card">
      <div className="flex items-center gap-2 border-b border-border px-4 py-3">
        <Gauge className="h-4 w-4 text-muted-foreground" />
        <div>
          <h3 className="text-sm font-semibold text-foreground">Pulse background reviews</h3>
          <p className="text-xs text-muted-foreground">
            {reviews.length} {reviews.length === 1 ? 'Pulse pass' : 'Pulse passes'} linked to this retained run
          </p>
        </div>
      </div>
      <div className="divide-y divide-border/70">
        {reviews.map(review => (
          <details key={`${review.run_id}-${review.session_id}`} className="group/pulse">
            <summary className="flex cursor-pointer list-none items-center gap-3 px-4 py-3 hover:bg-accent/30">
              <ChevronRight className="h-4 w-4 shrink-0 text-muted-foreground transition-transform group-open/pulse:rotate-90" />
              <div className="min-w-0 flex-1">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="text-sm font-medium text-foreground">Pulse review</span>
                  <span className="rounded border border-border bg-muted/50 px-1.5 py-0.5 text-[10px] font-medium text-muted-foreground">
                    {review.trigger_source === 'manual' || review.schedule_id === 'manual-pulse' ? 'Manual' : 'Scheduled'}
                  </span>
                  <span className="text-xs text-muted-foreground">{review.agents.length} {review.agents.length === 1 ? 'agent' : 'agents'}</span>
                </div>
                <div className="mt-1 text-xs text-muted-foreground">
                  {formatStartedAt(review.started_at)}
                </div>
              </div>
            </summary>
            <div className="space-y-3 border-t border-border/70 px-4 py-3">
              {review.agents.map(agent => {
                const failed = agent.status === 'failed' || Boolean(agent.error)
                const running = agent.status === 'running'
                return (
                  <details key={agent.agent_id} className="group/agent overflow-hidden rounded-md border border-border bg-background/70">
                    <summary className="flex cursor-pointer list-none items-center gap-3 px-3 py-2.5 hover:bg-accent/30">
                      <ChevronRight className="h-3.5 w-3.5 shrink-0 text-muted-foreground transition-transform group-open/agent:rotate-90" />
                      {running ? <Loader2 className="h-4 w-4 animate-spin text-sky-600 dark:text-sky-300" /> : failed ? <XCircle className="h-4 w-4 text-destructive" /> : <CheckCircle className="h-4 w-4 text-emerald-500" />}
                      <div className="min-w-0 flex-1">
                        <div className="truncate text-sm font-medium text-foreground">{agent.name || agent.agent_id}</div>
                        <div className="mt-0.5 flex flex-wrap gap-x-2 text-[11px] text-muted-foreground">
                          {agent.started_at && <span>{formatStartedAt(agent.started_at)}</span>}
                          {agent.duration && <span>{agent.duration}</span>}
                          {(agent.provider || agent.model_id) && <span>{[agent.provider, agent.model_id].filter(Boolean).join(' / ')}</span>}
                        </div>
                      </div>
                      <span className="text-[10px] font-medium capitalize text-muted-foreground">{agent.status}</span>
                    </summary>
                    <div className="space-y-3 border-t border-border p-3">
                      {agent.parent_execution_id && (
                        <div className="text-[11px] text-muted-foreground">
                          Parent agent: <span className="font-mono text-foreground/80">{agent.parent_execution_id}</span>
                        </div>
                      )}
                      {agent.transcript_status && agent.transcript_status !== 'ok' && (
                        <div className="rounded border border-amber-500/25 bg-amber-500/5 p-2 text-xs text-amber-700 dark:text-amber-300">
                          Transcript: {agent.transcript_status}
                        </div>
                      )}
                      {(agent.events || []).map((event, index) => (
                        <div key={`${agent.agent_id}-${index}`} className="overflow-hidden rounded border border-border/80">
                          <div className="flex items-center gap-2 border-b border-border/70 bg-muted/20 px-3 py-1.5 text-[11px] font-semibold text-muted-foreground">
                            {event.type === 'user_message' ? <User className="h-3 w-3" /> : event.type === 'tool_call' ? <Terminal className="h-3 w-3" /> : <Bot className="h-3 w-3" />}
                            {event.type === 'user_message' ? 'Instruction' : event.type === 'tool_call' ? 'Tool call' : 'Agent response'}
                            {event.timestamp && <span className="ml-auto normal-case font-normal">{formatStartedAt(event.timestamp)}</span>}
                          </div>
                          {event.type === 'tool_call' && event.tool_call ? (
                            <StructuredJsonView value={event.tool_call} label="Tool call details" collapsed={false} />
                          ) : (
                            <div className="p-3 text-sm leading-relaxed text-foreground">
                              <MarkdownRenderer content={event.text || ''} />
                            </div>
                          )}
                        </div>
                      ))}
                      {agent.error && (
                        <div className="rounded border border-destructive/25 bg-destructive/5 p-3 text-sm text-destructive">{agent.error}</div>
                      )}
                      {agent.result && (
                        <details className="group/result rounded border border-border/80 bg-muted/10">
                          <summary className="cursor-pointer list-none px-3 py-2 text-xs font-semibold text-foreground">Final result</summary>
                          <div className="border-t border-border/70 p-3 text-sm leading-relaxed"><MarkdownRenderer content={agent.result} /></div>
                        </details>
                      )}
                    </div>
                  </details>
                )
              })}
            </div>
          </details>
        ))}
      </div>
    </section>
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
