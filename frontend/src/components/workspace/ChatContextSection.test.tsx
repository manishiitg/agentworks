// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { describe, expect, it, vi } from 'vitest'
import { renderToStaticMarkup } from 'react-dom/server'
import { agentApi } from '../../services/api'
import { cacheText, costHeading, isFocusedChat, PartsBlock, tokenShare } from './ChatContextSection'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

// Real failure mode: subscription-equivalent spend shown as one plain dollar figure, as if the person was charged it.
describe('costHeading', () => {
  const tokens = { new_input: 0, cache_read: 0, cache_write: 0, output: 0, thinking: 0 }
  const period = { providers: ['claude-code'], apps: ['Claude'], from: '2026-11-18T10:00:00Z', to: '2026-11-21T10:00:00Z', total_usd: 1.2, helper_usd: 0, calls: 3, tokens, models: [], cache_percent: 0 }
  const base = { total_usd: 9.2, current: period, unpriced_calls: 0 }
  it('shows a provider figure as plain cost', () => {
    expect(costHeading({ ...base, basis: 'actual' })).toEqual({ label: 'Cost so far', value: '$1.20' })
  })
  it('labels subscription-equivalent spend as an estimate you are not charged', () => {
    const heading = costHeading({ ...base, basis: 'estimate' })
    expect(heading.value).toBe('about $1.20')
    expect(heading.note).toMatch(/not charged/i)
  })
  it('says mixed spend is partly estimated', () => {
    expect(costHeading({ ...base, basis: 'mixed' }).label).toMatch(/partly estimated/)
  })
  // Real report: a chat moved from Codex to Claude showed the GPT spend as its cost; the heading is the current app only.
  it('heads a chat that moved apps with the spend since the move', () => {
    const heading = costHeading({ ...base, basis: 'actual', earlier: { ...period, providers: ['codex-cli'], apps: ['Codex'], total_usd: 8 } })
    expect(heading.label).toMatch(/^Cost since /)
    expect(heading.value).toBe('$1.20')
  })
})

// Real report: "saved $786" next to "$71 spent" read as nonsense without what it would have cost.
describe('cacheText', () => {
  const tokens = { new_input: 0, cache_read: 0, cache_write: 0, output: 0, thinking: 0 }
  const period = { providers: ['claude-code'], from: '', to: '', total_usd: 71, helper_usd: 0, calls: 3, tokens, models: [], cache_percent: 98 }
  it('says what the chat would have cost without the cache', () => {
    expect(cacheText({ ...period, cache_saved_usd: 786 })).toMatch(/would have cost about \$857\.00.*saved about \$786\.00/)
    expect(cacheText(period)).toBe('98% of input came from cache.')
    expect(cacheText({ ...period, cache_percent: 0 })).toBe('')
  })
})

// Real failure mode: a real but tiny share of tokens shown as 0%.
describe('tokenShare', () => {
  it('never shows a real share as 0%', () => {
    expect(tokenShare(4, 1000)).toBe('under 1%')
    expect(tokenShare(500, 1000)).toBe('50%')
    expect(tokenShare(0, 1000)).toBe('')
  })
})

// Real failure mode: estimated parts shown as exact figures.
describe('PartsBlock', () => {
  it('labels the breakdown as an estimate and lists every part', () => {
    const html = renderToStaticMarkup(<PartsBlock parts={[{ key: 'tools', label: 'Tools', tokens: 2000 }, { key: 'resent', label: 'Instructions sent again', tokens: 3000, note: 'Sent twice.' }, { key: 'messages', label: 'Conversation', tokens: 8000 }]} />)
    expect(html).toContain('(estimate)')
    expect(html).toContain('Tools')
    expect(html).toContain('Conversation')
    expect(html).toContain('about')
    expect(html).toContain('Sent twice.')
  })
  // #4181: closed <details> must not mount PartItems or fetch transcripts until expanded.
  it('fetches an openable part only after the person expands it', async () => {
    const spy = vi.spyOn(agentApi, 'getChatContextPart').mockResolvedValue({ items: [{ label: 'AGENTS.md', text: 'rules', tokens: 120 }] })
    const host = document.createElement('div')
    document.body.append(host)
    const root = createRoot(host)
    try {
      await act(async () => {
        root.render(<PartsBlock sessionId="sess-1" parts={[{ key: 'skills', label: 'Skills list', tokens: 500, openable: true }]} />)
      })
      expect(spy).not.toHaveBeenCalled()
      const details = host.querySelector('details')!
      await act(async () => {
        details.open = true
        details.dispatchEvent(new Event('toggle'))
      })
      expect(spy).toHaveBeenCalledTimes(1)
      expect(spy).toHaveBeenCalledWith('sess-1', 'skills')
    } finally {
      await act(async () => root.render(null))
      host.remove()
      spy.mockRestore()
    }
  })
})
describe('isFocusedChat', () => {
  it('shows only the chat the person is in', () => {
    expect(isFocusedChat('a', 'a')).toBe(true)
    expect(isFocusedChat('a', 'b')).toBe(false)
    expect(isFocusedChat('a', null)).toBe(false)
    expect(isFocusedChat('', '')).toBe(false)
  })
})
