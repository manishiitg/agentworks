import React from 'react'

// The backend marks only native automatic continuation inputs. Never classify
// a person's message by its text, even if they paste an internal prompt.
export function isAutomaticContinueNotice(kind: unknown): boolean {
  return ['background_command_finished', 'background_work_finished', 'app_restarted', 'usage_limit_reset'].includes(String(kind))
}

export function BackgroundWakeNotice({ metadata, details = '' }: { metadata?: Record<string, unknown>; details?: string }) {
  const kind = metadata?.system_notice_kind
  if (!isAutomaticContinueNotice(kind)) return null
  const continuation = kind === 'app_restarted' || kind === 'usage_limit_reset'
  const failed = metadata?.background_failed === true
  const output = continuation ? details : typeof metadata?.background_output === 'string' ? metadata.background_output : ''
  const label = kind === 'background_command_finished' ? 'Background command' : 'Background work'
  return <div className="my-2 text-xs text-muted-foreground" role="status">
    <div>{kind === 'app_restarted' ? '↻ App restarted, the agent is continuing' : kind === 'usage_limit_reset' ? '⏱ Usage limit reset, the agent is continuing' : <>{failed ? '✗' : '✓'} {label} finished{failed ? ' with an error' : ''}, the agent is continuing</>}</div>
    {output && <details className="mt-1">
      <summary className="cursor-pointer hover:text-foreground">{continuation ? 'Show details' : 'Show output'}</summary>
      <pre className="mt-2 max-h-64 overflow-auto whitespace-pre-wrap break-words font-mono text-xs">{output}</pre>
    </details>}
  </div>
}
