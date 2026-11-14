import React from 'react'

// The backend marks only native automatic continuation inputs. Never classify
// a person's message by its text, even if they paste an internal prompt.
export function BackgroundWakeNotice({ metadata }: { metadata?: Record<string, unknown> }) {
  const kind = metadata?.system_notice_kind
  if (kind !== 'background_command_finished' && kind !== 'background_work_finished') return null
  const failed = metadata?.background_failed === true
  const output = typeof metadata?.background_output === 'string' ? metadata.background_output : ''
  const label = kind === 'background_command_finished' ? 'Background command' : 'Background work'
  return <div className="my-2 text-xs text-muted-foreground" role="status">
    <div>{failed ? '✗' : '✓'} {label} finished{failed ? ' with an error' : ''}, the agent is continuing</div>
    {output && <details className="mt-1">
      <summary className="cursor-pointer hover:text-foreground">Show output</summary>
      <pre className="mt-2 max-h-64 overflow-auto whitespace-pre-wrap break-words font-mono text-xs">{output}</pre>
    </details>}
  </div>
}
