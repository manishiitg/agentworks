import { useBotsPageStore } from './botsPageStore'
import { useProjectBots } from './useProjectBots'

/** Bots added to this project, in its agent list (#4545): each opens your own private chat with it. */
export function ProjectBotRows({ projectId }: { projectId: string }) {
  const value = useProjectBots(projectId)
  const openChat = useBotsPageStore(state => state.openChat)
  if (!value?.bots.length) return null
  return <>{value.bots.map(bot => <div key={bot.id} role="listitem">
    <button type="button" onClick={() => openChat(bot.id, { projectId })} title={bot.purpose ? `${bot.purpose} · from ${bot.project_title}` : `From ${bot.project_title}`}
      className="flex h-[30px] w-full min-w-0 items-center gap-2 rounded-lg px-2 text-left text-[13px] text-foreground/80 hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring max-md:min-h-10">
      <span aria-hidden="true" className="w-4 shrink-0 text-center text-xs">{bot.icon || '🤖'}</span>
      <span className="min-w-0 flex-1 truncate">{bot.name}</span>
      <span className="shrink-0 rounded-full bg-muted px-1.5 text-[10px] leading-4 text-muted-foreground">bot</span>
    </button>
  </div>)}</>
}
