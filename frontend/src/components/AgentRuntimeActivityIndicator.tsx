import { LoadingIndicator } from './ui/LoadingIndicator'
import { memo } from 'react'
import type { ChatRuntimeActivity } from '../utils/chatRuntimeActivity'

// Only lifecycle changes rerender the animation; token arrival does not.
export const AgentRuntimeActivityIndicator = memo(function AgentRuntimeActivityIndicator({ state, label }: ChatRuntimeActivity) {
  if (state === 'ready') return null
  return (
    <span
      data-testid="agent-runtime-activity"
      className="inline-flex h-4 shrink-0 items-center"
      role="status"
      aria-label={label}
      title={label}
    >
      {state === 'running'
        ? <LoadingIndicator className="h-3 w-3 text-info" aria-hidden="true" />
        : <span className="h-2 w-2 rounded-full bg-[hsl(var(--warning))]" aria-hidden="true" />}
    </span>
  )
})
