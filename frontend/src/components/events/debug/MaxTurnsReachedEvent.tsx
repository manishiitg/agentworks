import React from 'react'
import type { MaxTurnsReachedEvent } from '../../../generated/events'

interface MaxTurnsReachedEventDisplayProps {
  event: MaxTurnsReachedEvent
}

export const MaxTurnsReachedEventDisplay: React.FC<MaxTurnsReachedEventDisplayProps> = ({
  event
}) => {
  return (
    <div className="bg-muted/50 border border-border rounded p-2">
      <div className="flex items-start gap-3">
        <div className="flex-1 min-w-0">
          <div className="flex items-center gap-2">
            <span className="text-sm font-medium text-foreground">
              Max Turns Reached
            </span>
            {event.max_turns && (
              <span className="text-xs text-muted-foreground">
                {event.max_turns} turns
              </span>
            )}
          </div>
          
          {event.question && (
            <div className="text-xs text-muted-foreground mt-1">
              {event.question.length > 60 ? `${event.question.substring(0, 60)}...` : event.question}
            </div>
          )}

          <div className="space-y-2 mt-2">
            {event.turn !== undefined && (
              <div>
                <span className="text-xs font-medium text-muted-foreground">Turn:</span>
                <span className="ml-2 text-xs text-foreground">{event.turn}</span>
              </div>
            )}

            {event.agent_mode && (
              <div>
                <span className="text-xs font-medium text-muted-foreground">Agent Mode:</span>
                <span className="ml-2 text-xs text-foreground">{event.agent_mode}</span>
              </div>
            )}

            {event.final_message && (
              <div>
                <span className="text-xs font-medium text-muted-foreground">Final Message:</span>
                <div className="mt-1 p-2 bg-muted rounded text-xs text-foreground max-h-32 overflow-y-auto">
                  {event.final_message}
                </div>
              </div>
            )}

            {event.timestamp && (
              <div>
                <span className="text-xs font-medium text-muted-foreground">Time:</span>
                <span className="ml-2 text-xs text-foreground">
                  {new Date(event.timestamp).toLocaleTimeString()}
                </span>
              </div>
            )}
          </div>
        </div>
      </div>
    </div>
  )
}
