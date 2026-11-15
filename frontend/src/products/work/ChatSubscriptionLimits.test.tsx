// @vitest-environment happy-dom
import { expect, it, vi } from 'vitest'
import { renderToStaticMarkup } from 'react-dom/server'
vi.mock('./useChatContextView', () => ({ useChatContextView: () => ({session_id:'chat',used_tokens:288000,window_tokens:1000000,limits:[{label:'5h',used_percent:40},{label:'7d',used_percent:72,resets_at:'2026-11-20T12:00:00Z'}]}) }))
vi.mock('../../services/api', () => ({ agentApi: {} }))
vi.mock('../../hooks/useChatPreferences', () => ({ useChatPreferences: vi.fn() }))
vi.mock('../../stores/useChatStore', () => ({ useChatStore: {} }))
import ChatStatusBar from './ChatStatusBar'
import { chatLimitsPercent, PlanWindowRow } from './ChatContextItems'

it('shows every reported subscription window, not just the fullest, without inventing absent windows', () => {
 const html=renderToStaticMarkup(<ChatStatusBar sessionId="chat" busy={false}/>)
 expect(html).toContain('5h 40% · 7d 72%')
 expect(chatLimitsPercent([{label:'5h',used_percent:0}])).toBe('5h 0%')
 expect(chatLimitsPercent(undefined)).toBe('')
 const detail=renderToStaticMarkup(<PlanWindowRow label="7d" percent={72} resets="Friday 12:00"/>)
 expect(detail).toContain('72% used · 28% left')
 expect(detail).toContain('Resets Friday 12:00')
})
