import { isAxiosError } from 'axios'
import api from '../services/api'

/** Registered by server.go; shared by browser actions calling the external tool API. */
export const externalCallPath = '/api/external/v1/call'

export async function callExternal(name: string, args: Record<string, unknown>) {
  try {
    return await api.post(externalCallPath, { name, arguments: args })
  } catch (cause) {
    if (isAxiosError(cause) && cause.response?.status === 404) {
      const code = (cause.response.data as { error?: { code?: string } } | undefined)?.error?.code
      if (!code || code === 'unknown_tool') throw new Error('This action is not available in this version.')
    }
    throw cause
  }
}
