import React from 'react'

// The backend marks only native automatic continuation inputs. Never classify
// a person's message by its text, even if they paste an internal prompt.
export function isAutomaticContinueNotice(kind: unknown): boolean {
  return ['background_command_finished', 'background_work_finished', 'app_restarted', 'usage_limit_reset', 'plan_open'].includes(String(kind))
}

export function BackgroundWakeNotice({ metadata, details = '' }: { metadata?: Record<string, unknown>; details?: string }) {
  const kind = metadata?.system_notice_kind
  if (!isAutomaticContinueNotice(kind)) return null
  const continuation = kind === 'app_restarted' || kind === 'usage_limit_reset' || kind === 'plan_open'
  const failed = metadata?.background_failed === true
  const output = continuation ? details : typeof metadata?.background_output === 'string' ? metadata.background_output : ''
  const label = kind === 'background_command_finished' ? 'Background command' : 'Background work'
  const line = kind === 'app_restarted' ? '↻ App restarted, the agent is continuing' : kind === 'usage_limit_reset' ? '⏱ Usage limit reset, the agent is continuing' : kind === 'plan_open' ? '↻ The plan still has open steps, the agent is continuing' : `${failed ? '✗' : '✓'} ${label} finished${failed ? ' with an error' : ''}, the agent is continuing`
  // One quiet line; the details open from the same line (owner, 26 Nov: the restart took too much room).
  if (!output) return <div className="my-1.5 text-xs text-muted-foreground" role="status" data-testid="background-wake-notice">{line}</div>
  return <details className="my-1.5 text-xs text-muted-foreground" role="status" data-testid="background-wake-notice">
    <summary className="cursor-pointer list-none hover:text-foreground [&::-webkit-details-marker]:hidden">{line} <span className="underline-offset-2 hover:underline">· {continuation ? 'details' : 'output'}</span></summary>
    <pre className="mt-2 max-h-64 overflow-auto whitespace-pre-wrap break-words font-mono text-xs">{output}</pre>
  </details>
}
