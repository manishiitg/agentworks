import { Users, AlertTriangle, CheckCircle2 } from 'lucide-react'
import type { CrewPlanStep } from '../../../utils/stepConfigMatching'
import { useCrewAttachmentAlias, useCrewTrigger } from './useCrewStepLookups'

function destinationLabel(runDestination: string): string {
  return runDestination === 'isolated'
    ? 'Fresh chat — every run starts a new conversation for this trigger'
    : 'Main Crew chat — the turn joins the shared crew conversation'
}

export function CrewStepDetailSection({ step, workspacePath }: { step: CrewPlanStep; workspacePath: string | null }) {
  const profileId = step.crew_profile_id || 'work'
  const trigger = useCrewTrigger(profileId, step.crew_project_id, step.trigger_id)
  const attachmentAlias = useCrewAttachmentAlias(workspacePath, step.crew_project_id)
  const outputFile = Array.isArray(step.context_output) ? step.context_output[0] : step.context_output || 'response.md'

  return (
    <>
      <section className="border-b border-border px-4 py-3 last:border-b-0">
        <div className="mb-2 flex items-center gap-2 text-xs font-semibold uppercase tracking-[0.12em] text-muted-foreground">
          <Users className="h-3.5 w-3.5" />
          Crew
        </div>
        <dl className="space-y-2 text-xs">
          <div className="flex items-baseline gap-2">
            <dt className="w-20 shrink-0 text-muted-foreground">Project</dt>
            <dd className="truncate font-mono text-foreground" title={step.crew_project_id}>{attachmentAlias ?? step.crew_project_id}</dd>
          </div>
          <div className="flex items-baseline gap-2">
            <dt className="w-20 shrink-0 text-muted-foreground">Trigger</dt>
            <dd className="min-w-0 text-foreground">
              {trigger === undefined ? (
                <span className="text-muted-foreground">Loading…</span>
              ) : trigger ? (
                <span className="font-medium">{trigger.name}</span>
              ) : (
                <span className="truncate font-mono" title={step.trigger_id}>{step.trigger_id}</span>
              )}
            </dd>
          </div>
          {trigger === null && (
            <div className="flex items-start gap-2 rounded-md border border-amber-500/25 bg-amber-500/5 px-2.5 py-2 text-[11px] leading-relaxed text-muted-foreground">
              <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0 text-amber-500" />
              <span>The trigger was not found or is no longer shared with you. The run will fail before starting until the Builder rebinds it.</span>
            </div>
          )}
          {trigger && (
            <div className="flex items-baseline gap-2">
              <dt className="w-20 shrink-0 text-muted-foreground">Retains</dt>
              <dd className="text-foreground/85">{destinationLabel(trigger.run_destination)}</dd>
            </div>
          )}
          {trigger && !trigger.enabled && (
            <div className="flex items-start gap-2 rounded-md border border-amber-500/25 bg-amber-500/5 px-2.5 py-2 text-[11px] leading-relaxed text-muted-foreground">
              <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0 text-amber-500" />
              <span>This trigger is disabled. Enable it in the Crew before running.</span>
            </div>
          )}
          <div className="flex items-baseline gap-2">
            <dt className="w-20 shrink-0 text-muted-foreground">Output</dt>
            <dd className="truncate font-mono text-foreground/80" title={outputFile}>{outputFile}</dd>
          </div>
          <div className="flex items-baseline gap-2">
            <dt className="w-20 shrink-0 text-muted-foreground">Timeout</dt>
            <dd className="text-foreground/80">{step.timeout_seconds ? `${step.timeout_seconds}s` : '30m default'}</dd>
          </div>
          {step.next_step_id && (
            <div className="flex items-baseline gap-2">
              <dt className="w-20 shrink-0 text-muted-foreground">Next</dt>
              <dd className="truncate font-mono text-foreground/80">{step.next_step_id}</dd>
            </div>
          )}
        </dl>
      </section>

      {step.instruction && (
        <section className="border-b border-border px-4 py-3 last:border-b-0">
          <div className="mb-2 flex items-center gap-2 text-xs font-semibold uppercase tracking-[0.12em] text-muted-foreground">
            <Users className="h-3.5 w-3.5" />
            Instruction
          </div>
          <p className="whitespace-pre-wrap text-xs leading-relaxed text-foreground/85">{step.instruction}</p>
        </section>
      )}

      <section className="border-b border-border px-4 py-3 last:border-b-0">
        <div className="mb-2 flex items-center gap-2 text-xs font-semibold uppercase tracking-[0.12em] text-muted-foreground">
          <Users className="h-3.5 w-3.5" />
          Crew files
        </div>
        {attachmentAlias === undefined ? (
          <p className="text-xs text-muted-foreground">Loading…</p>
        ) : attachmentAlias ? (
          <div className="space-y-1.5 text-xs">
            <div className="flex items-center gap-2 text-foreground">
              <CheckCircle2 className="h-3.5 w-3.5 shrink-0 text-emerald-500" />
              <span>Attached to open as <span className="font-mono">{attachmentAlias}</span></span>
            </div>
            <p className="leading-relaxed text-muted-foreground">
              Downstream steps read crew files as <span className="font-mono">{attachmentAlias}/&lt;path&gt;</span>.
              Files stay in the crew — nothing is copied into this run.
            </p>
          </div>
        ) : (
          <div className="flex items-start gap-2 rounded-md border border-amber-500/25 bg-amber-500/5 px-2.5 py-2 text-[11px] leading-relaxed text-muted-foreground">
            <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0 text-amber-500" />
            <span>Not attached. Ask the Builder to attach this crew before running — the run will fail fast until then.</span>
          </div>
        )}
      </section>
    </>
  )
}
