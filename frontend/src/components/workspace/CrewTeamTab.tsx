import { CrewTeamCard } from '../../products/work/CrewTeamCard'
import { MacSharedAgents } from '../../products/work/MacSharedAgents'
import { TeamWhenMacOff } from '../../products/work/TeamWhenMacOff'

/** Existing Team controls; Team-specific features live here, separate from the Access shell. */
export function CrewTeamTab({ projectId, connection, network }: { projectId: string; connection: string; network: string }) {
  return <>
    <CrewTeamCard projectId={projectId} />
    <MacSharedAgents projectId={projectId} connection={connection} network={network} />
    <TeamWhenMacOff projectId={projectId} connection={connection} network={network} />
  </>
}
