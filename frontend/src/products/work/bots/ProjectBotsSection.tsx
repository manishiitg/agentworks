import { Bot as BotIcon, Plus } from 'lucide-react'
import { WorkspaceCompactGroup } from '../../../components/workspace/WorkspaceCompactGroup'
import { WorkspaceContextRow as Row } from '../../../components/workspace/WorkspaceContextCard'
import { useState } from 'react'
import { useBotsPageStore } from './botsPageStore'
import { botError, setProjectBot } from './botsApi'
import { useProjectBots } from './useProjectBots'
import { BotAvatar } from './BotAvatar'

/** The right menu's "Bots in this project" (#4545): other projects' bots added here, each opening your own chat with it, and Add a bot (the Bots directory). */
export function ProjectBotsSection({ projectId }: { projectId: string }) {
  const value = useProjectBots(projectId)
  const show = useBotsPageStore(state => state.show)
  const openChat = useBotsPageStore(state => state.openChat)
  const [error, setError] = useState('')
  if (!value) return null
  const gone = value.unavailable.length
  const removeGone = async () => {
    setError('')
    try { for (const bot of value.unavailable) await setProjectBot(projectId, bot, false) } catch (reason) { setError(botError(reason, 'Could not remove it.')) }
  }
  const count = value.bots.length
  return <WorkspaceCompactGroup title="Bots in this project" icon={BotIcon} detail={count ? String(count) : 'none yet'}>
    {value.bots.map(bot => <Row key={bot.id} icon={BotIcon} detail={bot.project_title} hint={bot.purpose} onClick={() => openChat(bot.id, { projectId })}><BotAvatar bot={bot} connection={bot.connection} className="mr-1 inline-grid h-4 w-4 align-[-3px] text-[10px]" />{bot.name}</Row>)}
    {gone > 0 && <p className="px-1 text-[11px] text-muted-foreground">{gone === 1 ? 'One bot here is' : `${gone} bots here are`} no longer shared with you, so this project's agents can't ask {gone === 1 ? 'it' : 'them'}. Ask its maker to share it again, or <button type="button" className="text-primary underline" onClick={() => void removeGone()}>remove {gone === 1 ? 'it' : 'them'}</button>.</p>}
    {error && <p role="alert" className="px-1 text-[11px] text-destructive">{error}</p>}
    <Row icon={Plus} onClick={() => show()}>Add a bot</Row>
  </WorkspaceCompactGroup>
}
