import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { llmConfigService } from '../services/llm-config-api'
import { getApiBaseUrl } from '../services/api'
import { useAuthStore } from '../stores/useAuthStore'

export function useChatPreferences() {
  const userId = useAuthStore(state => state.user?.id)
  const queryClient = useQueryClient()
  const queryKey = ['chat-preferences', getApiBaseUrl(), userId]
  const query = useQuery({
    queryKey,
    queryFn: llmConfigService.getChatPreferences,
    enabled: Boolean(userId),
    staleTime: 30_000,
  })
  const save = useMutation({
    mutationFn: llmConfigService.setChatPreferences,
    onSuccess: value => queryClient.setQueryData(queryKey, value),
  })
  // A Crew's agents in the left menu (default) or as tabs (owner, 4 Nov).
  const agentsView = query.data?.agents_view === 'tabs' ? 'tabs' as const : 'menu' as const
  return { showContextIndicator: query.data?.show_context_indicator ?? false, agentsView, ready: query.isSuccess, save }
}
