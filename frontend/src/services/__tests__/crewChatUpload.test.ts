// @vitest-environment happy-dom
import { afterEach, expect, it, vi } from 'vitest'
import type { InternalAxiosRequestConfig } from 'axios'

vi.mock('../llm-config-api', () => ({}))
import api, { agentApi } from '../api'

const originalAdapter = api.defaults.adapter
afterEach(() => {
  api.defaults.adapter = originalAdapter
  vi.restoreAllMocks()
})

it('keeps a screenshot as multipart bytes through the authenticated chat client', async () => {
  const screenshot = new File([new Uint8Array([137, 80, 78, 71])], 'screenshot.png', { type: 'image/png' })
  let request: InternalAxiosRequestConfig | undefined
  api.defaults.adapter = async config => {
    request = config
    return { config, status: 200, statusText: 'OK', headers: {}, data: { filepath: 'Crew/example/uploads/screenshot.png' } }
  }

  await agentApi.uploadCrewChatFile(screenshot, 'example')
  expect(request?.url).toBe('/api/agent-profiles/work/projects/example/uploads')
  expect(request?.headers.getContentType()).toBe('multipart/form-data')
  expect(request?.data).toBeInstanceOf(FormData)
  const uploaded = (request?.data as FormData).get('file') as File
  expect(uploaded.name).toBe('screenshot.png')
  expect(new Uint8Array(await uploaded.arrayBuffer())).toEqual(new Uint8Array([137, 80, 78, 71]))
})
