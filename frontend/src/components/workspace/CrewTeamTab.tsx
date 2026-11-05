import { CrewTeamCard } from '../../products/work/CrewTeamCard'
import { MacSharedAgents } from '../../products/work/MacSharedAgents'
import { useProjectTeam } from '../../products/work/teamApi'
import { TeamSyncPanel } from '../../products/work/TeamSyncPanel'

/** The Access page's Team tab, in the approved mockup's order (owner, 4 Nov): where things live, the people and the
 * shared folder, then one row per agent. */
export function CrewTeamTab({ projectId, connection, network }: { projectId: string; connection: string; network: string }) {
  const { team, target } = useProjectTeam(projectId)
  // With the Team off the tab is only its card (owner, 5 Nov: "when team is off we should not show all this").
  if (!team?.on) return <CrewTeamCard projectId={projectId} />
  return <>
    <p className="mt-4 px-1 text-[13px] text-muted-foreground">People work with you here. The shared folder is kept on {network}. Messages to your Mac agents wait while your Mac is off and arrive when it is back. Publish an agent to keep it running on the server.</p>
    <CrewTeamCard projectId={projectId} />
    <MacSharedAgents projectId={projectId} connection={connection} />
    {/* The shared folder's sync history lives here, under Access (owner, 5 Nov), not as its own side-panel item. */}
    {team.folder_access && target && <TeamSyncPanel target={target} name={team.name} projectId={projectId} />}
  </>
}
