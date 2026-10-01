import { describe, expect, it } from 'vitest'
import type { ActiveSessionInfo, RuntimePhase, RuntimeSnapshot } from '../services/api-types'
import { chatRuntimeActivity } from './chatRuntimeActivity'

function session(phase: RuntimePhase, fields: Partial<RuntimeSnapshot> = {}): ActiveSessionInfo {
  return {
    session_id: 'chat-a', observer_id: '', agent_mode: 'multi-agent', status: 'running', last_activity: '', created_at: '',
    runtime_state: {
      session_id: 'chat-a', generation: 1, revision: 1, phase,
      foreground_turn: { busy: false, has_cancel: false, can_steer: false, synthetic: false },
      background_live: false, terminal_busy: false, waiting_for_user: false,
      last_progress_at: '', started_at: '', observed_at: '', ...fields,
    },
  }
}

describe('open chat runtime activity', () => {
  it('shows an accepted turn before the session cache refreshes', () => {
    expect(chatRuntimeActivity({ isStreaming: true }).state).toBe('running')
    expect(chatRuntimeActivity({ isStreaming: true }, session('idle')).state).toBe('running')
  })
  it('settles immediately despite a stale running cache', () => {
    expect(chatRuntimeActivity({ isCompleted: true }, session('running')).state).toBe('ready')
  })
  it.each(['running', 'waiting'] as const)('a completed foreground settles stale %s tab and cache flags', phase => {
    expect(chatRuntimeActivity({ foregroundTurnCompleted: true, isStreaming: true, isCompleted: false }, session(phase)))
      .toEqual({ state: 'ready', label: 'idle' })
  })
  it('keeps actual background work visible after a foreground completion', () => {
    expect(chatRuntimeActivity({ foregroundTurnCompleted: true, isStreaming: true }, session('idle', { background_live: true })))
      .toEqual({ state: 'running', label: 'background running' })
  })
  it.each(['idle', 'completed', 'failed', 'canceled'] as const)('does not spin for a retained %s CLI', phase => {
    expect(chatRuntimeActivity({}, session(phase)).state).toBe('ready')
  })
  it('uses authoritative starting state before tab streaming recovers', () => {
    expect(chatRuntimeActivity({}, session('starting')).state).toBe('running')
  })
  it('shows background work after the foreground finishes', () => {
    expect(chatRuntimeActivity({ isCompleted: true, hasRunningBgAgents: true }).label).toBe('background running')
    expect(chatRuntimeActivity({}, session('idle', { background_live: true })).state).toBe('running')
  })
  it('shows input waiting distinctly even when the tab still says streaming', () => {
    expect(chatRuntimeActivity({ isStreaming: true }, session('waiting', { waiting_for_user: true })))
      .toEqual({ state: 'waiting', label: 'waiting for input' })
  })
  it('does not infer work from a stopped legacy session', () => {
    expect(chatRuntimeActivity({}, { ...session('idle'), runtime_state: undefined, status: 'stopped' }).state).toBe('ready')
  })
})
