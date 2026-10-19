import axios from 'axios'
import { getApiBaseUrl, getAuthToken } from '../services/api'
import { assertChatIdentityCurrent, captureChatIdentity } from '../utils/chatIdentity'
import type { CrewFunctionSchema } from './crewFunctions'

export interface FunctionReference {
  name: string
  description?: string
  input_schema?: CrewFunctionSchema
  result_schema?: CrewFunctionSchema
}

export async function listFunctionReferences(target: string, query = ''): Promise<FunctionReference[]> {
  const identity = captureChatIdentity()
  const token = getAuthToken()
  const response = await axios.get<{ functions: FunctionReference[] }>('/api/function-references', {
    baseURL: getApiBaseUrl(),
    headers: token ? { Authorization: `Bearer ${token}` } : {},
    params: { target, q: query },
  })
  assertChatIdentityCurrent(identity)
  return response.data.functions
}
