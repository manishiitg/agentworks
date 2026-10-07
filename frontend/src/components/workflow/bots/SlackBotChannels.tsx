import { useCallback, useEffect, useState } from 'react'
import { Hash, Loader2, Lock, Plus, RefreshCw, Trash2 } from 'lucide-react'
import { Button } from '../../ui/Button'
import { Input } from '../../ui/Input'
import { agentApi } from '../../../services/api'
import type { SlackBotChannel } from '../../../services/api-types'
import { slackErrorText, type SlackDestination } from './SlackSlugsSection'

// The channels where one target answers through one bot (PLAT-668). A bot
// answers only in channels added here. Channels are picked by name from the
// ones the bot is in (listed server-side with the bot token), and every add is
// checked with Slack first: the channel exists, the bot and the person adding
// it are members.

const CHANNEL_RE = /^[CG][A-Z0-9]{2,}$/

function channelLabel(channel: SlackBotChannel): string {
  return channel.name ? `#${channel.name}` : channel.id
}

export function SlackBotChannels({ botId, botName, destination, readOnly }: {
  botId: string
  botName: string
  destination: SlackDestination
  readOnly: boolean
}) {
  const [channels, setChannels] = useState<SlackBotChannel[] | null>(null)
  const [listError, setListError] = useState<string | null>(null)
  const [picked, setPicked] = useState('')
  const [pasted, setPasted] = useState('')
  const [busy, setBusy] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)

  const destinationKey = `${destination.profile_id || ''}|${destination.workspace_path}`
  const load = useCallback(async () => {
    try {
      const listed = await agentApi.listSlackBotChannels(botId, destination)
      setChannels(listed.channels || [])
      setListError(listed.error || null)
    } catch (err) {
      setChannels([])
      setListError(slackErrorText(err, 'Could not list the bot’s channels'))
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [botId, destinationKey])
  useEffect(() => { void load() }, [load])

  const place = async (channelId: string, key: string) => {
    setBusy(key)
    setError(null)
    setNotice(null)
    try {
      const saved = await agentApi.placeSlackBotChannel(botId, channelId, destination, false)
      if (saved.channel_name) setNotice(`Answers in #${saved.channel_name}.`)
      await load()
      return true
    } catch (err) {
      setError(slackErrorText(err, 'The channel was not added'))
      return false
    } finally {
      setBusy(null)
    }
  }
  const remove = async (channel: SlackBotChannel) => {
    setBusy(`remove:${channel.id}`)
    setError(null)
    try {
      await agentApi.removeSlackBotChannelRoute(botId, channel.id, destination)
      await load()
    } catch (err) {
      setError(slackErrorText(err, 'The channel was not removed'))
    } finally {
      setBusy(null)
    }
  }

  if (channels === null) {
    return <div className="flex items-center gap-2 text-xs text-muted-foreground"><Loader2 className="h-3.5 w-3.5 animate-spin" />Loading channels…</div>
  }
  const answering = channels.filter(channel => channel.answers)
  const addable = channels.filter(channel => !channel.answers && channel.name)
  const pick = addable.find(channel => channel.id === picked) || addable[0]

  return (
    <div className="space-y-2">
      {answering.length === 0 && <p className="text-xs text-muted-foreground">No channels yet.</p>}
      {answering.map(channel => {
        const others = channel.targets.filter(target => !(target.workspace_path === destination.workspace_path && (target.profile_id || '') === (destination.profile_id || '')))
        return (
          <div key={channel.id} className="flex min-w-0 flex-wrap items-center gap-2 rounded-md border border-border bg-background p-2 text-sm">
            {channel.is_private ? <Lock className="h-4 w-4 shrink-0 text-muted-foreground" aria-label="Private channel" /> : <Hash className="h-4 w-4 shrink-0 text-muted-foreground" aria-label="Channel" />}
            <span className="font-semibold text-foreground" title={channel.id}>{channel.name || channel.id}</span>
            <span className="min-w-0 flex-1 truncate text-[11px] text-muted-foreground" title={others.map(target => target.label || target.slug).join(', ')}>
              everyone here can ask and run it (Run mode){others.length > 0 ? ` · shared with ${others.map(target => target.label || target.slug).join(', ')}` : ''}
            </span>
            {channel.removable && (
              <Button
                type="button"
                variant="ghost"
                size="xs"
                onClick={() => void remove(channel)}
                disabled={readOnly || !!busy}
                className="px-1 text-muted-foreground hover:bg-red-500/10 hover:text-red-600"
                aria-label={`Stop answering in ${channelLabel(channel)}`}
              >
                {busy === `remove:${channel.id}` ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Trash2 className="h-3.5 w-3.5" />}
              </Button>
            )}
          </div>
        )
      })}

      <div className="flex flex-wrap items-center gap-2">
        <select
          aria-label={`A channel @${botName} is in`}
          value={pick?.id || ''}
          onChange={e => setPicked(e.target.value)}
          disabled={readOnly || !!busy || addable.length === 0}
          className="h-8 min-w-0 flex-1 rounded-md border border-border bg-background px-2 text-sm"
        >
          {addable.length === 0 && <option value="">No other channels</option>}
          {addable.map(channel => <option key={channel.id} value={channel.id}>{channelLabel(channel)}</option>)}
        </select>
        <Button variant="outline" size="sm" onClick={() => pick && void place(pick.id, 'add')} disabled={readOnly || !!busy || !pick}>
          {busy === 'add' ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Plus className="h-3.5 w-3.5" />}
          Add channel
        </Button>
        <Button variant="ghost" size="xs" onClick={() => void load()} disabled={!!busy} aria-label="Reload channels" title="Reload channels">
          <RefreshCw className="h-3.5 w-3.5" />
        </Button>
      </div>
      <p className="text-[11px] text-muted-foreground">Channel not listed? Run <code className="rounded bg-muted px-1 font-mono">/invite @{botName}</code> in that channel first.</p>
      {listError && <p className="text-xs text-amber-700 dark:text-amber-300">{listError}</p>}
      {notice && <p className="text-xs text-emerald-700 dark:text-emerald-300">{notice}</p>}
      {error && <p className="text-xs text-red-600 dark:text-red-400">{error}</p>}

      <details className="text-xs">
        <summary className="cursor-pointer select-none text-muted-foreground">Paste a channel ID instead</summary>
        <div className="mt-2 flex items-center gap-2">
          <Input
            aria-label="Slack channel ID"
            value={pasted}
            onChange={e => setPasted(e.target.value.toUpperCase().replace(/[^A-Z0-9]/g, ''))}
            placeholder="C1234567890"
            disabled={readOnly || !!busy}
            className="h-8 min-w-0 flex-1 font-mono text-xs"
          />
          <Button
            variant="outline"
            size="sm"
            onClick={() => void place(pasted, 'paste').then(ok => { if (ok) setPasted('') })}
            disabled={readOnly || !!busy || !CHANNEL_RE.test(pasted)}
          >
            Add
          </Button>
        </div>
      </details>
    </div>
  )
}
