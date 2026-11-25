import React from 'react'

interface RoutingResponse {
  selected_route_id?: string
  reasoning?: string
}

interface RoutingRoute {
  route_id?: string
  route_name?: string
  condition?: string
  next_step_id?: string
}

interface RoutingEvaluatedEventData {
  step_id?: string
  step_index?: number
  step_title?: string
  step_path?: string
  routing_question?: string
  routing_response?: RoutingResponse
  routes?: RoutingRoute[]
  run_folder?: string
  workspace_path?: string
}

interface RoutingEvaluatedEventDisplayProps {
  event: RoutingEvaluatedEventData
  compact?: boolean
}

export const RoutingEvaluatedEventDisplay: React.FC<RoutingEvaluatedEventDisplayProps> = ({
  event,
  compact = false
}) => {
  const selectedRouteId = event.routing_response?.selected_route_id
  const reasoning = event.routing_response?.reasoning
  const selectedRoute = event.routes?.find(r => r.route_id === selectedRouteId)

  return (
    <div className={`bg-muted/50 border border-border rounded-md ${compact ? 'p-2' : 'p-3'}`}>
      <div className={`${compact ? 'text-xs' : 'text-sm'} text-foreground`}>
        <div className="font-medium flex items-center gap-2">
          <span>🔀 Routing Evaluated</span>
          {selectedRoute && (
            <span className="px-2 py-0.5 rounded text-xs font-semibold bg-muted text-foreground">
              {selectedRoute.route_name || selectedRouteId}
            </span>
          )}
        </div>

        {event.step_title && (
          <div className={`${compact ? 'text-[10px]' : 'text-xs'} text-muted-foreground mt-1`}>
            Step: {event.step_title}
          </div>
        )}

        {event.routing_question && (
          <div className={`${compact ? 'text-[10px]' : 'text-xs'} text-muted-foreground mt-2`}>
            <div className="font-medium">Question:</div>
            <div className="mt-0.5">{event.routing_question}</div>
          </div>
        )}

        {selectedRoute && (
          <div className={`${compact ? 'text-[10px]' : 'text-xs'} text-muted-foreground mt-2`}>
            <div className="font-medium">Selected: {selectedRoute.route_name || selectedRouteId}</div>
            {selectedRoute.condition && (
              <div className="mt-0.5">Condition: {selectedRoute.condition}</div>
            )}
            {selectedRoute.next_step_id && (
              <div className="mt-0.5">Routes to: {selectedRoute.next_step_id}</div>
            )}
          </div>
        )}

        {reasoning && (
          <div className={`${compact ? 'text-[10px]' : 'text-xs'} text-muted-foreground mt-2`}>
            <div className="font-medium">Reasoning:</div>
            <div className="mt-0.5 whitespace-pre-wrap">{reasoning}</div>
          </div>
        )}
      </div>
    </div>
  )
}
