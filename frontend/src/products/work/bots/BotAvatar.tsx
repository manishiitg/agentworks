import { Avatar, type AvatarFields } from '../../../components/ui/Avatar'

/** A bot's picture everywhere it shows (#4545): its photo or icon on a colour, the same avatar people have, else its
 * emoji. connection is the sign-in of the bot's server on a Mac, through which its photo is read. */
export function BotAvatar({ bot, connection, className = 'h-8 w-8 text-base' }: { bot: AvatarFields & { name: string; icon?: string }; connection?: string; className?: string }) {
  if (bot.avatar_url || bot.avatar_icon) return <Avatar name={bot.name} avatar_url={bot.avatar_url} avatar_icon={bot.avatar_icon} avatar_color={bot.avatar_color} connection={connection} className={className} />
  return <span aria-hidden="true" className={`inline-grid shrink-0 place-items-center rounded-full bg-primary/10 ${className}`}>{bot.icon || '🤖'}</span>
}
