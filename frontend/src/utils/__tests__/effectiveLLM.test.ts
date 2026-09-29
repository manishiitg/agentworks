import { describe, expect, it } from 'vitest'
import type { SavedLLM } from '../../services/api-types'
import { effectiveLLMUnderLock, effectiveProviderUnderLock, runtimeStatusLLMChoice } from '../effectiveLLM'
import { chatUsesStructuredTransport, shouldRouteChatInputToLiveTransport } from '../liveInputSubmission'

const published = [{ provider: 'cursor-cli', model_id: 'cursor-cli' }] as unknown as SavedLLM[]

describe('effectiveProviderUnderLock', () => {
  it('replaces a saved provider with the published one under a lock', () => {
    expect(effectiveProviderUnderLock('claude-code', true, published)).toBe('cursor-cli')
  })
  it('keeps a saved provider that is itself published', () => {
    expect(effectiveProviderUnderLock('Cursor-CLI', true, published)).toBe('Cursor-CLI')
  })
  it('is a pass-through without a lock or without a published list', () => {
    expect(effectiveProviderUnderLock('claude-code', false, published)).toBe('claude-code')
    expect(effectiveProviderUnderLock('claude-code', true, [])).toBe('claude-code')
    expect(effectiveProviderUnderLock(null, false, published)).toBeNull()
  })
  it('falls back to the published provider when nothing was saved', () => {
    expect(effectiveProviderUnderLock(null, true, published)).toBe('cursor-cli')
  })
  it('agrees with the full provider+model rule', () => {
    const full = effectiveLLMUnderLock({ provider: 'claude-code', model_id: 'claude-sonnet-5-5' }, true, published)
    expect(full?.provider).toBe(effectiveProviderUnderLock('claude-code', true, published))
  })

  it('keeps a product profile binding under a Cursor-only deployment lock', () => {
    const choice = { provider: 'claude-code', model_id: 'claude-sonnet-5-5' }
    expect(effectiveLLMUnderLock(choice, true, published, 'agent_profile')).toEqual({
      ...choice,
      forcedByLock: false,
    })
    expect(effectiveProviderUnderLock(choice.provider, true, published, 'agent_profile')).toBe(choice.provider)
  })

  it('delivers product follow-ups live before the session reports its transport', () => {
    const provider = effectiveProviderUnderLock('claude-code', true, published, 'agent_profile')
    const usesStructuredTransport = chatUsesStructuredTransport({
      isInteractiveWorkflowBuilder: false,
      reportedTransport: '',
      providerUsesStructuredTransport: provider === 'cursor-cli',
    })
    expect(shouldRouteChatInputToLiveTransport({
      hasSession: true,
      isCodingAgentProvider: true,
      isWorkflowMode: false,
      usesStructuredTransport,
    })).toBe(true)
  })

  it('still falls back when a product has no resolved binding', () => {
    expect(effectiveLLMUnderLock(null, true, published, 'agent_profile')?.provider).toBe('cursor-cli')
    expect(effectiveProviderUnderLock(null, true, published, 'agent_profile')).toBe('cursor-cli')
  })
})

describe('runtimeStatusLLMChoice', () => {
  const selected = { provider: 'claude-code', model_id: 'claude-sonnet-5-5' }
  const retainedRuntime = { provider: 'muse-cli', model_id: 'muse-spark-1.3-contributor' }

  it('shows an authoritative project or workflow selection instead of a retained stale runtime', () => {
    expect(runtimeStatusLLMChoice(selected, retainedRuntime, true)).toEqual(selected)
  })

  it('keeps the attached runtime authoritative for ordinary AgentWorks chats', () => {
    expect(runtimeStatusLLMChoice(selected, retainedRuntime, false)).toEqual(retainedRuntime)
  })

  it('falls back to the attached runtime when a product has no complete selection', () => {
    expect(runtimeStatusLLMChoice({ provider: 'claude-code' }, retainedRuntime, true)).toEqual(retainedRuntime)
  })
})
