import { useEffect, useState } from 'react'
import { SlackBotChoice } from '../../components/integrations/ProductSlackPanel'
import { crewSharePage, useTeamTab, type CrewShareSlack } from './teamApi'

// The existing Crew Slack setup, opened through Connections. Permissions and bot routes are unchanged.
export function CrewSlackSettings({ projectId, workspacePath }: { projectId: string; workspacePath?: string }) {
  const { target } = useTeamTab(projectId)
  const [slack, setSlack] = useState<CrewShareSlack>()
  useEffect(() => {
    if (!target) return
    let active = true
    const load = () => void crewSharePage(target).then(data => { if (active) setSlack(data.slack) }).catch(() => undefined)
    load()
    window.addEventListener('agentworks:team-updated', load)
    return () => { active = false; window.removeEventListener('agentworks:team-updated', load) }
  }, [target?.project, target?.connection]) // eslint-disable-line react-hooks/exhaustive-deps
  if (!workspacePath) return <p className="text-xs text-muted-foreground">Open this Crew to set up Slack.</p>
  return <SlackBotChoice workspacePath={workspacePath} noun="Crew" layout="cards" who={slack && <ShareSlackRules slack={slack} />} />
}

/** Connections Slack settings: Who can talk to it: the server's lines (who may message the Crew in a channel, in a direct message,
 * and who is refused), then its note. */
function ShareSlackRules({ slack }: { slack: CrewShareSlack }) {
  return <div className="space-y-1.5 text-[13.5px]">
    {slack.rules.map(rule => <p key={rule.line} className="flex gap-2">
      <span aria-hidden="true" className={rule.allowed ? 'text-emerald-600 dark:text-emerald-400' : 'text-muted-foreground'}>{rule.allowed ? '✓' : '✕'}</span>
      <span className={rule.allowed ? undefined : 'text-muted-foreground'}>{rule.where && <b>{rule.where} </b>}{rule.line}</span>
    </p>)}
    {slack.note && <p className="pt-1 text-xs text-muted-foreground">{slack.note}</p>}
  </div>
}

