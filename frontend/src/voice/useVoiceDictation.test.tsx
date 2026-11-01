// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
import { useVoiceDictation } from '../../shared/voice/useVoiceDictation'

vi.mock('../../shared/voice/pcmCapture', () => ({
  ensurePcmWorklet: vi.fn().mockResolvedValue(undefined),
  startPcmCapture: vi.fn().mockResolvedValue({ stop: vi.fn() }),
}))

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
afterEach(() => { vi.unstubAllGlobals(); vi.restoreAllMocks() })

// Home, setup and account navigation can unmount the recorder while the
// browser is still asking for mic access. Do not leave that mic recording.
it('releases the microphone when permission finishes after the composer unmounts', async () => {
  let grant!: (stream: MediaStream) => void
  const getUserMedia = vi.fn(() => new Promise<MediaStream>(resolve => { grant = resolve }))
  vi.stubGlobal('navigator', { mediaDevices: { getUserMedia } })
  const closeAudio = vi.fn().mockResolvedValue(undefined)
  vi.stubGlobal('AudioContext', class { state = 'running'; close = closeAudio })
  const closeSocket = vi.fn()
  vi.stubGlobal('WebSocket', class {
    static OPEN = 1
    readyState = 0
    close = closeSocket
  })
  let start!: () => Promise<void>
  function Recorder() {
    start = useVoiceDictation({ streamUrl: () => 'ws://localhost/voice' }).start
    return null
  }
  const host = document.createElement('div')
  const root = createRoot(host)
  await act(async () => root.render(<Recorder />))
  let recording!: Promise<void>
  await act(async () => { recording = start() })
  await act(async () => root.unmount())
  const stopTrack = vi.fn()
  await act(async () => {
    grant({ getTracks: () => [{ stop: stopTrack }] } as never)
    await recording
  })
  expect(stopTrack).toHaveBeenCalledOnce()
  expect(closeAudio).toHaveBeenCalledOnce()
  expect(closeSocket).toHaveBeenCalledOnce()
})
