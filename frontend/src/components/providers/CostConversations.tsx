import { lazy, Suspense, useEffect, useState } from 'react'
import type { ChatHistoryConversation, CostConversation } from '../../services/api-types'
import { formatCost, formatUSD } from '../workflow/costs/helpers'
import { totalTokens } from '../../utils/costTokens'
import CostPricingNotice from './CostPricingNotice'
import CostTokenBreakdown from './CostTokenBreakdown'
import { formatTokens } from '../../utils/formatTokens'

const ConversationRenderer = lazy(() => import('../ui/ConversationRenderer').then(module => ({ default: module.ConversationRenderer })))

function ChatPreview({ conversation }: { conversation: CostConversation }) {
  const [history, setHistory] = useState<ChatHistoryConversation | null>(null)
  const [error, setError] = useState('')
  const [offset, setOffset] = useState(0)
  const [loading, setLoading] = useState(false)
  useEffect(() => {
    let cancelled = false
    setHistory(null); setError(''); setLoading(true)
    import('../../services/api').then(({ agentApi }) => agentApi.getChatHistoryResumeConversation(conversation.session_id, conversation.workflow_id || undefined, 50, offset, false, true))
      .then(data => { if (!cancelled) setHistory(data) })
      .catch(err => { if (!cancelled) setError(err instanceof Error ? err.message : 'Chat history is unavailable.') })
      .finally(() => { if (!cancelled) setLoading(false) })
    return () => { cancelled = true }
  }, [conversation.session_id, conversation.workflow_id, offset])
  return <div className="mt-3 rounded-lg border border-border p-3">
    <h5 className="text-sm font-semibold">Chat history</h5>

    {loading && <p className="text-xs text-muted-foreground">Loading chat…</p>}
    {error && <p role="alert" className="text-xs text-red-600">Could not load chat: {error}</p>}
    {history && <>
      <div className="mb-3 rounded border border-border p-3">
        <h6 className="text-sm font-medium">Latest prompt size</h6>

        {!history.saved_prompt_sizes?.length && <p className="mt-2 text-xs text-muted-foreground">Not recorded.</p>}
        {history.saved_prompt_sizes?.map(prompt => <p key={prompt.role} className="mt-2 flex justify-between gap-3 text-xs">
          <span>{prompt.role === 'system' ? 'System prompt' : 'Developer instructions'}</span>
          <span className="font-mono">{prompt.character_count.toLocaleString()} characters</span>
        </p>)}
      </div>
      {history.conversation_history.length === 0 && <p className="text-xs text-muted-foreground">No saved messages are available for this conversation.</p>}
      <div className="max-h-[32rem] overflow-auto"><Suspense fallback={<p className="text-xs">Loading messages…</p>}><ConversationRenderer content={JSON.stringify(history)} /></Suspense></div>
      <div className="mt-2 flex gap-3 text-xs">
        {history.history_pagination?.has_more && <button className="text-primary underline" onClick={() => setOffset(history.history_pagination!.next_offset)}>Earlier messages</button>}
        {offset > 0 && <button className="text-primary underline" onClick={() => setOffset(0)}>Latest messages</button>}
      </div>
    </>}
  </div>
}

export default function CostConversations({ rows }: { rows: CostConversation[] }) {
  const [limit, setLimit] = useState(20)
  const [chat, setChat] = useState<string | null>(null)
  const [titles, setTitles] = useState<Record<string, string>>({})
  const ordered = [...rows].sort((a, b) => b.total_cost_usd - a.total_cost_usd || b.last_seen.localeCompare(a.last_seen))
  const paths = [...new Set(rows.map(row => row.workflow_id).filter(Boolean))].sort().join('\n')
  useEffect(() => {
    let cancelled = false
    setTitles({}); setLimit(20); setChat(null)
    void Promise.all(paths.split('\n').filter(Boolean).map(async path => {
      try { const { agentApi } = await import('../../services/api'); return (await agentApi.listChatHistorySessions(200, 0, path)).sessions.map(session => [`${path}:${session.session_id}`, session.title || session.session_id]) } catch { return [] }
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
        <button type="button" className="mt-3 text-xs text-primary underline" onClick={() => setChat(chat === key ? null : key)}>{chat === key ? 'Hide chat' : 'View chat'}</button>
        {chat === key && <ChatPreview key={key} conversation={row} />}
        <details className="mt-3 border-t border-border pt-3">
          <summary className="cursor-pointer text-xs text-muted-foreground hover:text-foreground">Per-turn details</summary>
          <div className="mt-2 max-h-[30rem] space-y-2 overflow-auto">
          {executions.map(([id, turn]) => <details key={id} className="rounded border border-border p-2">
            <summary className="cursor-pointer text-xs">{new Date(turn.first_seen).toISOString().replace('T', ' ').slice(0, 19)} UTC · {turn.scope.replaceAll('_', ' ')} <span className="float-right font-mono">{turn.total_cost_usd === 0 && turn.unpriced_call_count ? 'Not priced' : formatUSD(turn.total_cost_usd)}</span></summary>
            <div className="mt-2"><CostTokenBreakdown compact usage={turn} /></div>
            <p className="mt-2 text-xs text-muted-foreground">Models: {Object.keys(turn.by_model || {}).join(', ') || 'Not recorded'}</p>
            <div className="mt-1"><CostPricingNotice usage={turn} /></div>
          </details>)}
          </div>
        </details>
      </details>
    })}
    {ordered.length > limit && <button type="button" className="text-xs text-primary underline" onClick={() => setLimit(current => current + 20)}>More conversations</button>}
  </section>
}
