import { describe, expect, it } from 'vitest'
import type { ActiveSessionInfo } from '../../services/api-types'
import type { ChatTab } from '../../stores/useChatStore'
import { workProjectActivity, workSideChatDisplayName } from './workProjectPresentation'

const session = (id: string, patch: Partial<ActiveSessionInfo> = {}) => ({ session_id: id, workspace_path: 'Crew/project', display_status: 'busy', status: 'running', ...patch }) as ActiveSessionInfo
const tabs = [{ sessionId: 'main' }, { sessionId: 'side' }] as ChatTab[]
describe('project activity presentation', () => {
  it('does not repeat activity already represented by an open tab', () => {
    expect(workProjectActivity([session('main'), session('side')], tabs, 'project')).toBeNull()
  })
  it('retains hidden server activity and input requests, scoped to the exact project', () => {
    expect(workProjectActivity([session('hidden')], tabs, 'project')).toBe('running')
    expect(workProjectActivity([session('hidden', { needs_user_input: true })], tabs, 'project')).toBe('waiting')
    expect(workProjectActivity([session('hidden', { workspace_path: 'Crew/other' })], tabs, 'project')).toBeNull()
    expect(workProjectActivity([session('hidden', { display_status: 'idle', status: 'completed' })], tabs, 'project')).toBeNull()
  })
  it('keeps server-reported background-only work visible after foreground completion', () => {
    expect(workProjectActivity([session('main', { display_status: 'idle', status: 'waiting', has_running_background_agents: true })], tabs, 'project')).toBe('running')
  })
  it('recognizes the legacy Code scope after a move without changing session identity', () => {
    const legacy = session('code:project:project:chat:hidden', { workspace_path: '_users/owner/Chats/Code/projects/project' })
    expect(workProjectActivity([legacy], tabs, 'project')).toBe('running')
    expect(legacy.session_id).toBe('code:project:project:chat:hidden')
  })
})
it('uses saved names and makes only legacy hexadecimal/empty labels readable without renaming', () => {
  expect(workSideChatDisplayName('Quarterly review')).toBe('Quarterly review')
  expect(workSideChatDisplayName('Chat b72fd6e2')).toBe('Untitled chat')
  expect(workSideChatDisplayName(' ')).toBe('Untitled chat')
  expect(workSideChatDisplayName('Chat 2')).toBe('Chat 2')
  expect(workSideChatDisplayName('Chat about deployments')).toBe('Chat about deployments')
})
