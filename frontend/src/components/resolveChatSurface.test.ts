import { describe, it, expect } from 'vitest'
import { isHistoryPending, resolveChatSurface, resolveWorkflowChatSurface, type ChatSurfaceInputs } from './resolveChatSurface'

// Baseline = a fresh New Chat: nothing loading, no content, no read-only run.
const base: ChatSurfaceInputs = {
  isRestoring: false,
  resumeSettling: false,
  hasContent: false,
  isStreaming: false,
  hasRestoredLiveContent: false,
  isReadOnlyRunView: false,
}

describe('resolveChatSurface', () => {
  it('New Chat (no content, no restore) → landing', () => {
    expect(resolveChatSurface(base)).toBe('landing')
  })

  it('active content → active', () => {
    expect(resolveChatSurface({ ...base, hasContent: true })).toBe('active')
  })

  it('streaming → active', () => {
    expect(resolveChatSurface({ ...base, isStreaming: true })).toBe('active')
  })

  it('isRestoring + empty → restoring', () => {
    expect(resolveChatSurface({ ...base, isRestoring: true })).toBe('restoring')
  })

  it('resume-empty (resumeSettling) → restoring', () => {
    expect(resolveChatSurface({ ...base, resumeSettling: true })).toBe('restoring')
  })

  it('resume-with-execution/terminal content → active', () => {
    expect(resolveChatSurface({ ...base, hasRestoredLiveContent: true })).toBe('active')
  })

  // --- Native/terminal resume (the terminal-only resume flicker fix) ---
  // A native/terminal resume reattaches a live terminal but leaves NO
  // execution-tree nodes; hasRestoredLiveContent carries the terminal-presence
  // signal so the surface stays on the terminal instead of flipping to landing.
  it('resumed tab with a terminal (no execution tree) → active', () => {
    expect(resolveChatSurface({ ...base, hasRestoredLiveContent: true })).toBe('active')
  })

  it('resumed tab with neither terminal nor execution content after settle → landing', () => {
    expect(
      resolveChatSurface({ ...base, resumeSettling: false, hasRestoredLiveContent: false }),
    ).toBe('landing')
  })

  it('resumed tab still settling → restoring (not landing)', () => {
    expect(resolveChatSurface({ ...base, resumeSettling: true })).toBe('restoring')
  })

  it('resumed tab still settling but terminal already present → active (not landing)', () => {
    expect(
      resolveChatSurface({ ...base, resumeSettling: true, hasRestoredLiveContent: true }),
    ).toBe('active')
  })

  it('content wins over a still-set restoring flag → active', () => {
    expect(
      resolveChatSurface({ ...base, isRestoring: true, hasContent: true }),
    ).toBe('active')
  })

  it('restored terminal content wins over restoring flag → active', () => {
    expect(
      resolveChatSurface({ ...base, isRestoring: true, hasRestoredLiveContent: true }),
    ).toBe('active')
  })

  // --- Read-only run view (scheduled/bot run) — the "schedule-bounce" fix ---
  it('read-only run with no events yet → restoring (never landing)', () => {
    expect(resolveChatSurface({ ...base, isReadOnlyRunView: true })).toBe('restoring')
  })

  it('read-only run with events → active', () => {
    expect(
      resolveChatSurface({ ...base, isReadOnlyRunView: true, hasContent: true }),
    ).toBe('active')
  })

  it('read-only run while streaming → active', () => {
    expect(
      resolveChatSurface({ ...base, isReadOnlyRunView: true, isStreaming: true }),
    ).toBe('active')
  })

  it('a normal fresh chat still → landing (read-only off)', () => {
    expect(resolveChatSurface({ ...base, isReadOnlyRunView: false })).toBe('landing')
  })

  // --- Resume coherence (Resume-first-click + New-Chat-from-running-conv) ---
  // A resume in flight (restoredConversationPath set) before its terminal/content
  // is detected must stay 'restoring', NOT flash 'landing'. The wiring keeps
  // resumeSettling true long enough (RESUME_SETTLE_MS) to outlast the terminal
  // presence probe so the resumed terminal shows on the FIRST Resume click.
  it('resume pending (terminal not yet detected, settle active) → restoring not landing', () => {
    expect(
      resolveChatSurface({
        ...base,
        resumeSettling: true,
        hasContent: false,
        isStreaming: false,
        hasRestoredLiveContent: false,
      }),
    ).toBe('restoring')
  })

  // New Chat from a RUNNING conversation: the fresh tab has rotated its sessionId
  // and cleared its per-tab streaming flag, so the resolver receives isStreaming
  // false even though the PRIOR session was streaming (the global state.isStreaming
  // must NOT be fed here). With no content/restore signals it resolves to landing.
  it('new chat after a prior session streamed (per-tab not streaming) → landing', () => {
    expect(
      resolveChatSurface({
        ...base,
        isStreaming: false, // per-tab flag for the fresh tab, not the lingering global
        hasContent: false,
        hasRestoredLiveContent: false,
        resumeSettling: false,
      }),
    ).toBe('landing')
  })

  // A genuinely streaming tab (its OWN per-tab flag true) stays active — the
  // per-tab scoping fix must not regress real streaming chats.
  it('genuinely streaming active tab (per-tab true) → active', () => {
    expect(resolveChatSurface({ ...base, isStreaming: true })).toBe('active')
  })

  // --- Synchronous resumePending (first-render-after-resume must be restoring) ---
  // ChatArea now feeds `resumeSettling` from a SYNCHRONOUS `resumePending` derived
  // in render (restoredConversationPath set, no content/stream, give-up timer not
  // elapsed). On the very first render after a Resume click this is true while the
  // terminal-presence probe is still pending, so the surface MUST be 'restoring',
  // never the transient 'landing' that previously canceled the resume.
  it('resume just set (probe pending, gaveUp not elapsed) → restoring NOT landing', () => {
    expect(
      resolveChatSurface({
        ...base,
        resumeSettling: true, // synchronous resumePending on the first resume render
        hasContent: false,
        isStreaming: false,
        hasRestoredLiveContent: false,
      }),
    ).toBe('restoring')
  })

  // Once the presence probe returns the restored terminal (the 2341-byte snapshot
  // case), hasRestoredLiveContent flips and the surface becomes 'active'.
  it('resume → active the moment the terminal-presence probe returns', () => {
    expect(
      resolveChatSurface({
        ...base,
        resumeSettling: true,
        hasRestoredLiveContent: true,
      }),
    ).toBe('active')
  })

  // Only after the give-up timer elapses (resumePending false) with no terminal and
  // no content does a dead resume fall to 'landing' — when the clear-on-landing
  // guard is allowed to fire.
  it('resume that gave up (resumePending false, still empty) → landing', () => {
    expect(
      resolveChatSurface({
        ...base,
        resumeSettling: false, // resumePending false after give-up timer
        hasContent: false,
        isStreaming: false,
        hasRestoredLiveContent: false,
      }),
    ).toBe('landing')
  })
})

describe('resolveWorkflowChatSurface', () => {
  it('keeps an idle workflow on its retained tmux terminal', () => {
    expect(resolveWorkflowChatSurface(base, true)).toBe('active')
  })

  it('keeps an actually empty workflow on Previous Chats', () => {
    expect(resolveWorkflowChatSurface(base, false)).toBe('landing')
  })

  it('hides the previous workflow while the selected workflow tab is resolving', () => {
    expect(resolveWorkflowChatSurface({ ...base, hasContent: true }, false, false)).toBe('restoring')
  })
})

describe('isHistoryPending (tab must not flash the new-chat landing while history loads)', () => {
  const tab = { sessionId: 's1', historyLoaded: false, hasEvents: false, isStreaming: false }
  it('session known, history not loaded yet, no events → pending (loading, not landing)', () => {
    expect(isHistoryPending(tab)).toBe(true)
    expect(resolveChatSurface({ ...base, isRestoring: isHistoryPending(tab) })).toBe('restoring')
  })
  it('a running chat still waits for its saved messages before showing an empty transcript', () => {
    expect(isHistoryPending({ ...tab, isStreaming: true })).toBe(true)
  })
  it('loaded and empty → not pending, landing shows', () => {
    expect(isHistoryPending({ ...tab, historyLoaded: true })).toBe(false)
    expect(resolveChatSurface({ ...base, isRestoring: isHistoryPending({ ...tab, historyLoaded: true }) })).toBe('landing')
  })
  it('brand-new tab without a session id → landing at once', () => {
    expect(isHistoryPending({ ...tab, sessionId: undefined })).toBe(false)
  })
})
