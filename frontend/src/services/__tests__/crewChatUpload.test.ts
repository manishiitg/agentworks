// @vitest-environment happy-dom
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import type { InternalAxiosRequestConfig } from 'axios'

vi.mock('../llm-config-api', () => ({}))
import api, { agentApi } from '../api'

const originalAdapter = api.defaults.adapter
beforeEach(() => {
  vi.stubGlobal('localStorage', { getItem: () => null, setItem: vi.fn(), removeItem: vi.fn() })
})
afterEach(() => {
  api.defaults.adapter = originalAdapter
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

it('sends a screenshot as chunks the server lacks, then finishes, through the authenticated chat client', async () => {
  const screenshot = new File([new Uint8Array([137, 80, 78, 71])], 'screenshot.png', { type: 'image/png' })
  const requests: InternalAxiosRequestConfig[] = []
  api.defaults.adapter = async config => {
    requests.push(config)
    const plan = config.url?.endsWith('/plan')
    const missing = plan ? JSON.parse(config.data as string).file.chunks.map((chunk: { id: string }) => chunk.id) : undefined
    return { config, status: 200, statusText: 'OK', headers: {}, data: plan ? { missing } : { filepath: 'Crew/example/uploads/screenshot.png' } }
  }

  const reply = await agentApi.uploadCrewChatFile(screenshot, 'example')
  expect(requests.map(request => request.url)).toEqual([
    '/api/agent-profiles/work/projects/example/uploads/plan',
    '/api/agent-profiles/work/projects/example/uploads/chunks',
    '/api/agent-profiles/work/projects/example/uploads',
  ])
  expect(reply.filepath).toBe('Crew/example/uploads/screenshot.png')
  const frames = new Uint8Array(requests[1].data as ArrayBuffer)
  expect(frames.length).toBe(36 + 4) // sha256, length, the four bytes
  expect(Array.from(frames.slice(32))).toEqual([0, 0, 0, 4, 137, 80, 78, 71])
  const finished = JSON.parse(requests[2].data as string)
  expect(finished.name).toBe('screenshot.png')
  expect(finished.file.size).toBe(4)
})
