import { useState } from 'react'
import type { AgentStartEvent } from '@/generated/events'
import { useExpandable } from '../useExpandable'
import { Plus, Minus } from 'lucide-react'
import { useLLMStore } from '@/stores'
import { getModelDisplayName } from '@/utils/llmDisplay'

interface AgentStartEventProps {
  event: AgentStartEvent
  compact?: boolean
}

export function AgentStartEventComponent({ event, compact = false }: AgentStartEventProps) {
  const { isExpanded, toggle } = useExpandable()
  const [isMetaExpanded, setIsMetaExpanded] = useState(false)
  const savedLLMs = useLLMStore(state => state.savedLLMs)
  const availableLLMs = useLLMStore(state => state.availableLLMs)
  const modelMetadataCatalog = useLLMStore(state => state.modelMetadataCatalog)
  const modelDisplayName = getModelDisplayName({
    provider: event.provider,
    modelId: event.model_id,
    metadata: modelMetadataCatalog,
    savedLLMs,
    availableLLMs,
  })

  const formatTimestamp = (timestamp?: string) => {
    if (!timestamp) return 'Unknown time'
    return new Date(timestamp).toLocaleTimeString()
  }

  const hasExpandableContent = event.model_id || event.provider || event.parent_id || event.trace_id

  return (
    <div className="bg-blue-50 dark:bg-blue-900/20 border border-blue-200 dark:border-blue-800 rounded p-2">
      <div className="flex items-center justify-between gap-3">
        {/* Left side: Icon and main content */}
        <div className="flex items-center gap-3 min-w-0 flex-1">
          <div className="min-w-0 flex-1">
            <div className="text-sm font-medium text-blue-700 dark:text-blue-300">
              🤖 Agent Started: {event.agent_type || 'Unknown'}
              {isMetaExpanded && (
                <span className="text-xs font-normal text-blue-600 dark:text-blue-400">
                  {!compact && event.use_code_execution_mode && ' | Code Exec'}
                  {' | Model: '}{modelDisplayName}
                  {' | Provider: '}{event.provider || 'Unknown'}
                </span>
              )}
            </div>
          </div>
        </div>

        {/* Right side: Time and expand button */}
        <div className="flex items-center gap-2 flex-shrink-0">
          {event.timestamp && (
            <div className="text-xs text-blue-600 dark:text-blue-400">
              {formatTimestamp(event.timestamp)}
            </div>
          )}
          
          <button
            onClick={() => setIsMetaExpanded(!isMetaExpanded)}
            className="p-0.5 hover:bg-blue-200 dark:hover:bg-blue-800 rounded text-blue-600 dark:text-blue-400 transition-colors"
            title={isMetaExpanded ? "Hide details" : "Show details"}
          >
            {isMetaExpanded ? <Minus className="w-3.5 h-3.5" /> : <Plus className="w-3.5 h-3.5" />}
          </button>

          {hasExpandableContent && (
            <button
              onClick={toggle}
              className="p-0.5 hover:bg-blue-200 dark:hover:bg-blue-800 rounded text-blue-600 dark:text-blue-400 transition-colors flex items-center gap-1"
              title={isExpanded ? "Collapse details (Alt+Click for all)" : "Expand details (Alt+Click for all)"}
            >
              <span className="text-[10px] uppercase font-bold">Details</span>
              {isExpanded ? <Minus className="w-3 h-3" /> : <Plus className="w-3 h-3" />}
            </button>
          )}
        </div>
      </div>

      {/* Expandable content */}
      {isExpanded && hasExpandableContent && (
        <div className="mt-3 space-y-2">
          {/* Model Information */}
          {(event.model_id || event.provider) && (
            <div className="bg-card border border-border rounded-md p-2">
              <div className="text-xs font-medium text-foreground mb-1">Model Details:</div>
              <div className="text-xs text-muted-foreground space-y-1">
                {event.model_id && (
                  <div>Model: <code className="bg-muted px-1 rounded">{modelDisplayName}</code></div>
                )}
                {event.provider && (
                  <div>Provider: <code className="bg-muted px-1 rounded">{event.provider}</code></div>
                )}
              </div>
            </div>
          )}

          {/* Hierarchy Information */}
          {event.parent_id && (
            <div className="bg-card border border-border rounded-md p-2">
              <div className="text-xs font-medium text-foreground mb-1">Hierarchy:</div>
              <div className="text-xs text-muted-foreground">
                Parent ID: <code className="bg-muted px-1 rounded">{event.parent_id}</code>
              </div>
            </div>
          )}

          {/* Trace Information */}
          {(event.trace_id || event.span_id || event.event_id) && (
            <div className="bg-card border border-border rounded-md p-2">
              <div className="text-xs font-medium text-foreground mb-1">Trace Info:</div>
              <div className="text-xs text-muted-foreground space-y-1">
                {event.trace_id && (
                  <div>Trace: <code className="bg-muted px-1 rounded">{event.trace_id}</code></div>
                )}
                {event.span_id && (
                  <div>Span: <code className="bg-muted px-1 rounded">{event.span_id}</code></div>
                )}
                {event.event_id && (
                  <div>Event: <code className="bg-muted px-1 rounded">{event.event_id}</code></div>
                )}
              </div>
            </div>
          )}
        </div>
      )}
    </div>
  )
}
