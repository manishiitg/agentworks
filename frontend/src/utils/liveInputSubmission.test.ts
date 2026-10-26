import { describe, expect, it, vi } from 'vitest'

import {
  chatOffersLiveTerminal,
  chatUsesStructuredTransport,
  createLiveInputSubmissionCoordinator,
  shouldRouteChatInputToLiveTransport,
  shouldAppendOptimisticLiveInputMessage,
  shouldRefreshSessionEventStream,
  shouldShowLiveTerminalControl,
} from './liveInputSubmission'

describe('chatUsesStructuredTransport', () => {
  it('keeps Workflow Builder on tmux even when a provider summary says structured', () => {
    expect(chatUsesStructuredTransport({
      isInteractiveWorkflowBuilder: true,
      reportedTransport: 'structured',
      providerUsesStructuredTransport: true,
    })).toBe(false)
  })

  it('uses the reported transport for ordinary chats', () => {
    expect(chatUsesStructuredTransport({
      isInteractiveWorkflowBuilder: false,
      reportedTransport: 'tmux',
      providerUsesStructuredTransport: true,
    })).toBe(false)
    expect(chatUsesStructuredTransport({
      isInteractiveWorkflowBuilder: false,
      reportedTransport: 'structured',
      providerUsesStructuredTransport: false,
    })).toBe(true)
  })
})

describe('shouldShowLiveTerminalControl', () => {
  it('shows the terminal for Workflow Builder inside the AgentWorks product shell', () => {
    expect(shouldShowLiveTerminalControl(true, true, true)).toBe(true)
  })

  it('keeps the compact product chat chrome for ordinary product chats', () => {
    expect(shouldShowLiveTerminalControl(true, true, false)).toBe(false)
  })

  it('allows an explicit product to reuse the live terminal switch', () => {
    expect(shouldShowLiveTerminalControl(true, true, false, true)).toBe(true)
  })
})

describe('shouldRouteChatInputToLiveTransport', () => {
  const base = {
    hasSession: true,
    isCodingAgentProvider: true,
    isWorkflowMode: true,
    usesStructuredTransport: false,
  }

  it('routes an interactive coding-agent chat through live input', () => {
    expect(shouldRouteChatInputToLiveTransport(base)).toBe(true)
  })

  it('routes structured Cursor follow-ups through the normal query path', () => {
    expect(shouldRouteChatInputToLiveTransport({ ...base, usesStructuredTransport: true })).toBe(false)
  })

  it('requires an existing backend session', () => {
    expect(shouldRouteChatInputToLiveTransport({ ...base, hasSession: false })).toBe(false)
  })
})

describe('createLiveInputSubmissionCoordinator', () => {
  it('executes a rapid duplicate live message exactly once', async () => {
    const submitLiveInput = createLiveInputSubmissionCoordinator()
    let resolve!: (value: boolean) => void
    const submit = vi.fn(() => new Promise<boolean>(done => { resolve = done }))

    const first = submitLiveInput('session-a', 'continue with this', submit)
    const duplicate = submitLiveInput('session-a', 'continue with this', submit)

    expect(duplicate).toBe(first)
    expect(submit).toHaveBeenCalledTimes(1)
    resolve(true)
    await expect(Promise.all([first, duplicate])).resolves.toEqual([true, true])
  })

  it('does not suppress different messages or a later intentional repeat', async () => {
    const submitLiveInput = createLiveInputSubmissionCoordinator()
    const submit = vi.fn(async (value: string) => value)

    await Promise.all([
      submitLiveInput('session-a', 'first', () => submit('first')),
      submitLiveInput('session-a', 'second', () => submit('second')),
    ])
    await submitLiveInput('session-a', 'first', () => submit('first-again'))

    expect(submit).toHaveBeenCalledTimes(3)
  })
})

describe('shouldRefreshSessionEventStream', () => {
  it('reattaches a product stream even when a stale connection object remains', () => {
    expect(shouldRefreshSessionEventStream(true, true)).toBe(true)
  })

  it('reattaches an AgentWorks stream when an old connection wrapper remains', () => {
    expect(shouldRefreshSessionEventStream(false, true)).toBe(true)
  })

  it('connects any surface when no stream exists', () => {
    expect(shouldRefreshSessionEventStream(false, false)).toBe(true)
  })
})

describe('shouldAppendOptimisticLiveInputMessage', () => {
  it('does not append a second copy when SSE delivered the acknowledged message first', () => {
    const events = [{
      type: 'user_message',
      data: {
        data: {
          content: 'how are you',
          metadata: { message_id: 'steer-123' },
        },
      },
    }]

    expect(shouldAppendOptimisticLiveInputMessage(events, 'steer-123')).toBe(false)
  })

  it('still appends for a different acknowledgement or before the backend echo arrives', () => {
    const events = [{
      type: 'user_message',
      data: { data: { content: 'same text', metadata: { message_id: 'older' } } },
    }]

    expect(shouldAppendOptimisticLiveInputMessage(events, 'newer')).toBe(true)
    expect(shouldAppendOptimisticLiveInputMessage([], 'newer')).toBe(true)
  })
})

describe('terminal availability after native protocol migration', () => {
  it('hides the terminal for native sessions while retaining real tmux sessions', () => {
    for (const transport of ['app-server', 'rpc', 'acp', 'structured']) {
      expect(chatOffersLiveTerminal(true, transport, true)).toBe(false)
    }
    expect(chatOffersLiveTerminal(true, 'tmux', true)).toBe(true)
    expect(chatOffersLiveTerminal(true, 'tmux', false)).toBe(false)
    expect(chatOffersLiveTerminal(true, '', false)).toBe(false)
    expect(chatOffersLiveTerminal(true, '', true)).toBe(true)
    expect(chatOffersLiveTerminal(false, 'tmux', true)).toBe(false)
  })
})
