import { useEffect, useState } from 'react'
import { agentApi } from '../../../services/api'
import type { SlackDestination } from './SlackSlugsSection'

// Where a bot answers (PLAT-668, owner 2026-10-07): Slack decides. A bot
// answers in every channel it is a member of and in DMs; invite turns it on,
// kick turns it off. One read-only line, with channel names read on the server
// (users.conversations with the bot token, which never reaches the browser).

const SHOWN = 4

export function SlackBotWhere({ botId, botName, destination, dmOnly = false }: {
  botId: string
  botName: string
  destination: SlackDestination
  dmOnly?: boolean
}) {
  const [names, setNames] = useState<string[] | null>(null)
  const key = `${botId}|${destination.profile_id || ''}|${destination.workspace_path}`
  useEffect(() => {
    if (dmOnly) return
    let live = true
    agentApi.listSlackBotChannels(botId, destination)
      .then(listed => { if (live) setNames((listed.channels || []).map(channel => channel.name).filter((name): name is string => !!name)) })
      .catch(() => { if (live) setNames([]) })
    return () => { live = false }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key, dmOnly])

  if (dmOnly) {
    return <p className="text-xs text-muted-foreground">Answers in DMs with @{botName}.</p>
  }
  const listed = names || []
  const shown = listed.slice(0, SHOWN).map(name => `#${name}`)
  const more = listed.length > SHOWN ? `, +${listed.length - SHOWN} more` : ''
  return (
    <p className="text-xs text-muted-foreground" aria-label="Where the bot answers">
      Answers in every channel @{botName} is in{shown.length > 0 ? ` (${shown.join(', ')}${more})` : ''} and in DMs.
    </p>
  )
}
