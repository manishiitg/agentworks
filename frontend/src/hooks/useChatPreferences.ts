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
  return { showContextIndicator: query.data?.show_context_indicator ?? false, ready: query.isSuccess, save }
}
