import { describe, expect, it } from 'vitest'
import {
  shouldKeepChatSessionSubscribed,
  shouldKeepWorkflowSessionSubscribed,
} from './workflowSessionSubscription'

describe('shouldKeepWorkflowSessionSubscribed', () => {
  it('keeps listening after the foreground turn settles while the backend session is active', () => {
    expect(shouldKeepWorkflowSessionSubscribed({
      isStreaming: false,
      hasRunningBackgroundAgents: false,
      isBackendActive: true,
    })).toBe(true)
  })

  it('keeps listening for foreground and background activity', () => {
    expect(shouldKeepWorkflowSessionSubscribed({
      isStreaming: true,
      hasRunningBackgroundAgents: false,
      isBackendActive: false,
    })).toBe(true)
    expect(shouldKeepWorkflowSessionSubscribed({
      isStreaming: false,
      hasRunningBackgroundAgents: true,
      isBackendActive: false,
    })).toBe(true)
  })

  it('allows a genuinely idle workflow session to disconnect', () => {
    expect(shouldKeepWorkflowSessionSubscribed({
      isStreaming: false,
      hasRunningBackgroundAgents: false,
      isBackendActive: false,
    })).toBe(false)
  })
})

describe('shouldKeepChatSessionSubscribed', () => {
  it('keeps the visible chat connected even after its turn completes', () => {
    expect(shouldKeepChatSessionSubscribed({
      isVisible: true,
      isStreaming: false,
      hasRunningBackgroundAgents: false,
      isBackendActive: false,
    })).toBe(true)
  })

  it('keeps a hidden chat connected only while it has real activity', () => {
    expect(shouldKeepChatSessionSubscribed({
      isVisible: false,
      isStreaming: true,
      hasRunningBackgroundAgents: false,
      isBackendActive: false,
    })).toBe(true)
    expect(shouldKeepChatSessionSubscribed({
      isVisible: false,
      isStreaming: false,
      hasRunningBackgroundAgents: true,
      isBackendActive: false,
    })).toBe(true)
    expect(shouldKeepChatSessionSubscribed({
      isVisible: false,
      isStreaming: false,
      hasRunningBackgroundAgents: false,
      isBackendActive: true,
    })).toBe(true)
  })

  it('disconnects a hidden completed chat', () => {
    expect(shouldKeepChatSessionSubscribed({
      isVisible: false,
      isStreaming: false,
      hasRunningBackgroundAgents: false,
      isBackendActive: false,
    })).toBe(false)
  })
})

describe('visible workflow Builder chat keeps its stream', () => {
  it('stays subscribed while visible even when idle-but-alive (live input into a retained CLI)', () => {
    // server A rts-pr-reviewer (Cursor): input typed into the idle retained CLI set
    // no streaming flag and the session was not backend-active, so the visible
    // Builder chat had no stream and the reply appeared only after a reload.
    expect(shouldKeepChatSessionSubscribed({ isVisible: true, isStreaming: false, hasRunningBackgroundAgents: false, isBackendActive: false })).toBe(true)
    expect(shouldKeepChatSessionSubscribed({ isVisible: false, isStreaming: false, hasRunningBackgroundAgents: false, isBackendActive: false })).toBe(false)
  })
  it('ChatArea applies the visible-chat rule to workflow tabs', async () => {
    const { readFileSync } = await import('node:fs')
    const source = readFileSync('src/components/ChatArea.tsx', 'utf8')
    const workflowBranch = source.slice(source.indexOf("if (tab.metadata?.mode === 'workflow') {"), source.indexOf('// Skip completed sessions (definitely done)'))
    expect(workflowBranch).toContain('shouldKeepChatSessionSubscribed({')
    expect(workflowBranch).toContain('isVisible: activeTabIdFromStore === tab.tabId')
  })
})
