import { useEffect, useState } from 'react'
import api, { agentApi } from '../services/api'
import { captureChatIdentity, isChatIdentityCurrent } from '../utils/chatIdentity'

/** The same effective main-chat choice that the server uses when Home sends a message. */
export function HomeAgentModel() {
  const [label, setLabel] = useState('Checking model…')
  const [revision, setRevision] = useState(0)
  useEffect(() => {
    const refresh = () => setRevision(value => value + 1)
    window.addEventListener('provider-connections-changed', refresh)
    return () => window.removeEventListener('provider-connections-changed', refresh)
  }, [])
  useEffect(() => {
    let active = true
    const identity = captureChatIdentity()
    // Empty open requests ensure the personal Crew but do not start an agent turn.
    void api.post<{ crew_id: string; conversation_key: string }>('/api/agentworks', {}).then(async ({ data }) => {
      if (!active || !isChatIdentityCurrent(identity)) return
      const state = await agentApi.getAgentProfileChatModelState('work', data.crew_id)
      if (!active || !isChatIdentityCurrent(identity)) return
      const main = state.effective_chat_models.find(model => model.conversation_key === data.conversation_key)
      setLabel(main?.display_label || main?.model_id || 'Review model in AI accounts')
    }).catch(() => { if (active && isChatIdentityCurrent(identity)) setLabel('Model unavailable') })
    return () => { active = false }
  }, [revision])
  return <span className="min-w-0 text-xs text-muted-foreground">Runs on <span className="font-medium text-foreground">{label}</span></span>
}
