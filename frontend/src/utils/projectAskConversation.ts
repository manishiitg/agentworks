import api from '../services/api'
import { useChatStore, waitForChatStoreHydration } from '../stores/useChatStore'
import { useGlobalPresetStore } from '../stores/useGlobalPresetStore'
import { assertChatIdentityCurrent, captureChatIdentity } from './chatIdentity'
import { hydrateTabEvents } from './sessionRestore'

export type ProjectChat = {
  product: string; project_id: string; workspace: string; mode: 'run' | 'builder'; profile_id: string
  conversation: { session_id: string; conversation_key: string; conversation_id?: string; provider?: string; connection_id?: string }
}

/** The backend resolves project, caller, role and durable conversation. The
 * browser presents that conversation and sends through the normal chat queue. */
export async function openProjectAskConversation(project: { product?: string; projectId?: string; workspacePath: string }, options?: { activate?: boolean; userView?: boolean }): Promise<string> {
  const identity = captureChatIdentity()
  await waitForChatStoreHydration()
  assertChatIdentityCurrent(identity)
  const request = { workspace: project.workspacePath, ...(options?.userView ? { as_user: true } : {}) }
  type Selection = { choose_agent: true; agents: { key: string; name: string }[]; message: string }
  let { data } = await api.post<ProjectChat | Selection>('/api/project-ask/conversation', request)
  if ('choose_agent' in data) {
    if (!data.agents.length) throw new Error(data.message)
    const agentKey = await chooseAskAgent(data.agents)
    assertChatIdentityCurrent(identity)
    if (!agentKey) throw new Error('Choose an agent when you are ready to ask.')
    data = (await api.post<ProjectChat | Selection>('/api/project-ask/conversation', { ...request, agent_key: agentKey })).data
    if ('choose_agent' in data) throw new Error(data.message)
  }
  const target = data
  assertChatIdentityCurrent(identity)
  if (project.projectId && target.project_id !== project.projectId) throw new Error('The project conversation changed. Reopen the dashboard.')
  return presentProjectAskConversation(target, options)
}

/** Present a server-admitted Ask conversation without sending a message. */
export async function presentProjectAskConversation(target: ProjectChat, options?: { activate?: boolean }): Promise<string> {
  const identity = captureChatIdentity()
  await waitForChatStoreHydration()
  assertChatIdentityCurrent(identity)
  const conversation = target.conversation
  const store = useChatStore.getState()
  if (!target.profile_id && !useGlobalPresetStore.getState().workflowPresets.some(preset => preset.id === target.project_id)) {
    await useGlobalPresetStore.getState().refreshPresets()
    assertChatIdentityCurrent(identity)
  }
  const metadata = target.profile_id ? {
    mode: 'multi-agent' as const, agentProfileId: target.profile_id, agentProfileBuilder: false,
    agentProfileWorkspace: target.workspace, agentProfileProjectId: target.project_id,
    agentProfileConversationKey: conversation.conversation_key, agentProfileConversationId: conversation.conversation_id,
    agentProfileChatContract: 'profile-v1' as const, agentProfileChatProvider: conversation.provider,
    agentProfileChatConnectionID: conversation.connection_id,
  } : {
    mode: 'workflow' as const, presetQueryId: target.project_id, phaseId: 'workflow-builder', phaseName: 'Chat',
    workshopMode: target.mode === 'run' ? 'run' as const : 'workshop' as const,
  }
  const tabId = await store.createChatTab('Chat', metadata, conversation.session_id, { activate: options?.activate !== false })
  assertChatIdentityCurrent(identity)
  if ((useChatStore.getState().tabEvents[conversation.session_id]?.length ?? 0) === 0) {
    await hydrateTabEvents(conversation.session_id, { workspacePath: target.workspace, fallbackToChatHistory: true, preferChatHistory: true })
    assertChatIdentityCurrent(identity)
  }
  return tabId
}

/** One native accessible picker for dashboard Ask, embedded Ask and report questions. */
function chooseAskAgent(agents: { key: string; name: string }[]): Promise<string | null> {
  return new Promise(resolve => {
    const dialog = document.createElement('dialog')
    dialog.className = 'w-full max-w-sm rounded-xl border border-border bg-popover p-5 text-foreground shadow-xl backdrop:bg-black/40'
    dialog.setAttribute('aria-label', 'Choose an agent')
    const title = document.createElement('h2'); title.textContent = 'Who should answer?'; title.className = 'mb-3 font-semibold'
    const select = document.createElement('select'); select.setAttribute('aria-label', 'Agent'); select.className = 'w-full rounded-md border border-input bg-background p-2'
    const empty = document.createElement('option'); empty.value = ''; empty.textContent = 'Choose an agent…'; select.append(empty)
    for (const agent of agents) { const option = document.createElement('option'); option.value = agent.key; option.textContent = agent.name; select.append(option) }
    const actions = document.createElement('div'); actions.className = 'mt-4 flex justify-end gap-2'
    const cancel = document.createElement('button'); cancel.textContent = 'Cancel'; cancel.className = 'rounded-md px-3 py-2'
    const open = document.createElement('button'); open.textContent = 'Open chat'; open.disabled = true; open.className = 'rounded-md bg-primary px-3 py-2 text-primary-foreground disabled:opacity-50'
    select.onchange = () => { open.disabled = !select.value }
    cancel.onclick = () => dialog.close('')
    open.onclick = () => dialog.close(select.value)
    dialog.onclose = () => { const result = dialog.returnValue || null; dialog.remove(); resolve(result) }
    actions.append(cancel, open); dialog.append(title, select, actions); document.body.append(dialog); dialog.showModal(); select.focus()
  })
}
