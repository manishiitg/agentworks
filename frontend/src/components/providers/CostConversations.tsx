import { lazy, Suspense, useEffect, useState } from 'react'
import type { ChatHistoryConversation, CostConversation } from '../../services/api-types'
import { formatUSD } from '../workflow/costs/helpers'
import { pricingCoverageText } from '../../utils/costTokens'
import CostTokenBreakdown from './CostTokenBreakdown'

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
    <p className="mb-2 text-xs text-muted-foreground">Saved messages and tool activity for this conversation. Cost rows below use the recorded time (UTC).</p>
    {loading && <p className="text-xs text-muted-foreground">Loading chat…</p>}
    {error && <p role="alert" className="text-xs text-red-600">Could not load chat: {error}</p>}
    {history && <>
      <div className="mb-3 rounded border border-border p-3">
        <h6 className="text-sm font-medium">Prompt size</h6>
        <p className="mt-1 text-xs text-muted-foreground">Latest saved instructions. Earlier runs may have used different instructions.</p>
        {!history.saved_prompt_sizes?.length && <p className="mt-2 text-xs text-muted-foreground">Prompt size was not recorded for this conversation.</p>}
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
    <h4 className="text-sm font-semibold text-foreground">Conversations behind this cost</h4>
    <p className="text-xs text-muted-foreground">Expand a conversation for the cost of each recorded turn or agent run, then view its chat. Cache totals come from provider usage; reasons for cache misses are not included in the report.</p>
    {ordered.slice(0, limit).map(row => {
      const key = `${row.workflow_id}:${row.user_id}:${row.session_id}:${row.source_platform ?? ""}`
      const executions = Object.entries(row.by_execution || {}).sort(([, a], [, b]) => b.first_seen.localeCompare(a.first_seen))
      return <details key={key} className="rounded-lg border border-border p-3">
        <summary className="cursor-pointer text-sm font-medium">
          {titles[`${row.workflow_id}:${row.session_id}`] || row.session_id} <span className="float-right ml-2 font-mono">{row.total_cost_usd === 0 && row.unpriced_call_count ? 'Not priced' : formatUSD(row.total_cost_usd)}</span>
          <span className="mt-1 block text-xs font-normal text-muted-foreground">{executions.length} recorded {executions.length === 1 ? 'turn / agent run' : 'turns / agent runs'} · {row.first_seen.slice(0, 10)} to {row.last_seen.slice(0, 10)} (UTC)</span>
        </summary>
        <div className="mt-3"><CostTokenBreakdown compact usage={row} /></div>
        {pricingCoverageText(row) && <p className="mt-2 text-xs text-muted-foreground">{pricingCoverageText(row)}</p>}
        <button type="button" className="mt-3 text-xs text-primary underline" onClick={() => setChat(chat === key ? null : key)}>{chat === key ? 'Hide chat' : 'View chat'}</button>
        {chat === key && <ChatPreview key={key} conversation={row} />}
        <div className="mt-3 max-h-[30rem] space-y-2 overflow-auto">
          {executions.map(([id, turn]) => <details key={id} className="rounded border border-border p-2">
            <summary className="cursor-pointer text-xs">{new Date(turn.first_seen).toISOString().replace('T', ' ').slice(0, 19)} UTC · {turn.scope.replaceAll('_', ' ')} <span className="float-right font-mono">{turn.total_cost_usd === 0 && turn.unpriced_call_count ? 'Not priced' : formatUSD(turn.total_cost_usd)}</span></summary>
            <div className="mt-2"><CostTokenBreakdown compact usage={turn} /></div>
            <p className="mt-2 text-xs text-muted-foreground">Models: {Object.keys(turn.by_model || {}).join(', ') || 'Not recorded'}</p>
            {pricingCoverageText(turn) && <p className="mt-1 text-xs text-muted-foreground">{pricingCoverageText(turn)}</p>}
          </details>)}
        </div>
      </details>
    })}
    {ordered.length > limit && <button type="button" className="text-xs text-primary underline" onClick={() => setLimit(current => current + 20)}>More conversations</button>}
  </section>
}
