import { useEffect, useState } from 'react'
import type { CostConversation } from '../../services/api-types'
import { formatCost, formatUSD } from '../workflow/costs/helpers'
import { totalTokens } from '../../utils/costTokens'
import CostPricingNotice from './CostPricingNotice'
import CostTokenBreakdown from './CostTokenBreakdown'
import { formatTokens } from '../../utils/formatTokens'

export default function CostConversations({ rows }: { rows: CostConversation[] }) {
  const [limit, setLimit] = useState(20)
  const [openTurns, setOpenTurns] = useState<Record<string, boolean>>({})
  const [titles, setTitles] = useState<Record<string, string>>({})
  const ordered = [...rows].sort((a, b) => b.total_cost_usd - a.total_cost_usd || b.last_seen.localeCompare(a.last_seen))
  const paths = [...new Set(rows.map(row => row.workflow_id).filter(Boolean))].sort().join('\n')
  useEffect(() => {
    let cancelled = false
    setTitles({}); setLimit(20)
    void Promise.all(paths.split('\n').filter(Boolean).map(async path => {
      try { const { agentApi } = await import('../../services/api'); return (await agentApi.listChatHistorySessions(200, 0, path)).sessions.map(session => [`${path}:${session.session_id}`, session.title && session.title !== session.session_id ? session.title : '']) } catch { return [] }
    })).then(groups => {
      if (!cancelled) setTitles(Object.fromEntries(groups.flat()))
    })
    return () => { cancelled = true }
  }, [paths])
  if (rows.length === 0) return null
  return <section className="mt-4 space-y-2">
    <h4 className="text-sm font-semibold text-foreground">Conversations</h4>

    {ordered.slice(0, limit).map(row => {
      const key = `${row.workflow_id}:${row.user_id}:${row.session_id}:${row.source_platform ?? ""}`
      const executions = Object.entries(row.by_execution || {}).sort(([, a], [, b]) => b.first_seen.localeCompare(a.first_seen))
      return <details key={key} className="rounded-lg border border-border p-3">
        <summary className="cursor-pointer text-sm font-medium">
          <span className="inline-flex w-[calc(100%-1rem)] min-w-0 items-start justify-between gap-3 align-top">
            <span className="min-w-0">
              <span className="block break-words">{titles[`${row.workflow_id}:${row.session_id}`] || (row.session_id.startsWith('work:project:') ? 'Main chat' : 'Untitled chat')}</span>
              <span className="mt-1 block text-xs font-normal text-muted-foreground">{executions.length} {executions.length === 1 ? 'turn' : 'turns'}</span>
            </span>
            <span className="shrink-0 text-right tabular-nums">
              <span className="block">{formatTokens(totalTokens(row))} tokens</span>
              <span className="mt-1 block text-xs font-normal text-muted-foreground" title={formatUSD(row.total_cost_usd)}>{row.total_cost_usd === 0 && row.unpriced_call_count ? 'Not priced' : formatCost(row.total_cost_usd)}{(row.token_estimate_cost_usd ?? 0) > 0 || (row.subscription_shadow_cost_usd ?? 0) > 0 ? ' estimated' : ''}</span>
            </span>
          </span>
        </summary>
        <div className="mt-3 flex flex-wrap items-center justify-between gap-2 text-xs text-muted-foreground">
          <span>{row.first_seen.slice(0, 10)} to {row.last_seen.slice(0, 10)} (UTC)</span>
          <CostPricingNotice usage={row} />
        </div>
        <details className="mt-3 text-xs">
          <summary className="w-fit cursor-pointer text-muted-foreground hover:text-foreground">Token usage</summary>
          <div className="mt-2"><CostTokenBreakdown compact usage={row} /></div>
        </details>
        <details className="mt-3 border-t border-border pt-3" onToggle={event => { const open = event.currentTarget.open; setOpenTurns(current => current[key] === open ? current : { ...current, [key]: open }) }}>
          <summary className="cursor-pointer text-xs text-muted-foreground hover:text-foreground">Per-turn details</summary>
          {openTurns[key] && <TurnDetails conversation={row} />}
        </details>
      </details>
    })}
    {ordered.length > limit && <button type="button" className="text-xs text-primary underline" onClick={() => setLimit(current => current + 20)}>More conversations</button>}
  </section>
}

function TurnDetails({ conversation }: { conversation: CostConversation }) {
  const [messages, setMessages] = useState<Record<string, string> | null>(null)
  const [status, setStatus] = useState('')
  const executions = Object.entries(conversation.by_execution || {}).sort(([, a], [, b]) => b.first_seen.localeCompare(a.first_seen))
  useEffect(() => {
    let cancelled = false
    setMessages(null)
    setStatus('')
    // This read has the chat's own access check, separate from cost visibility.
    void import('../../services/api').then(async ({ agentApi }) => {
      const result: Record<string, string> = {}
      for (let start = 0; start < executions.length; start += 100) {
        const page = await agentApi.getUsageTurnMessages(conversation.session_id, conversation.workflow_id, executions.slice(start, start + 100).map(([id]) => id))
        Object.assign(result, page.messages)
      }
      if (!cancelled) setMessages(result)
    }).catch(() => { if (!cancelled) setStatus('Message not shown') })
    return () => { cancelled = true }
  }, [conversation])
  return <div className="mt-2 max-h-[30rem] space-y-2 overflow-auto">
    {executions.map(([id, turn]) => <details key={id} className="rounded border border-border p-2">
      <summary className="cursor-pointer text-xs">
        <span className="inline-flex w-[calc(100%-1rem)] min-w-0 justify-between gap-3 align-top">
          <span className="min-w-0">
            <span className="line-clamp-2 break-words font-medium">{messages?.[id] || (messages ? 'Message not recorded' : status || 'Loading message…')}</span>
            <span className="mt-1 block text-muted-foreground">{new Date(turn.first_seen).toISOString().replace('T', ' ').slice(0, 19)} UTC · {turn.scope.replaceAll('_', ' ')}</span>
          </span>
          <span className="shrink-0 text-right tabular-nums">
            <span className="block">{formatTokens(totalTokens(turn))} tokens</span>
            <span className="mt-1 block text-muted-foreground" title={formatUSD(turn.total_cost_usd)}>{turn.total_cost_usd === 0 && turn.unpriced_call_count ? 'Not priced' : formatCost(turn.total_cost_usd)}{(turn.token_estimate_cost_usd ?? 0) > 0 || (turn.subscription_shadow_cost_usd ?? 0) > 0 ? ' estimated' : ''}</span>
          </span>
        </span>
      </summary>
      {messages?.[id] && <p className="mt-3 whitespace-pre-wrap break-words text-sm">{messages[id]}</p>}
      <div className="mt-2"><CostTokenBreakdown compact usage={turn} /></div>
      <p className="mt-2 text-xs text-muted-foreground">Models: {Object.keys(turn.by_model || {}).join(', ') || 'Not recorded'}</p>
      <div className="mt-1"><CostPricingNotice usage={turn} /></div>
    </details>)}
  </div>
}
