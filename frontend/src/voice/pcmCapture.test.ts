import { expect, it } from 'vitest'
import { createDownsampler } from '../../shared/voice/pcmCapture'

// A 16 kHz browser context delivered about a third of the audio on a live
// server; capture now runs at the device rate and converts here.
it('turns one second at 48 kHz into one second at 16 kHz, across chunk boundaries', () => {
  const out: number[] = []
  const down = createDownsampler(48000, 16000)
  for (let chunk = 0; chunk < 375; chunk++) down(new Float32Array(128).fill(0.5), s => out.push(s))
  expect(out).toHaveLength(16000)
  expect(out.every(s => Math.abs(s - 0.5) < 1e-6)).toBe(true)
  const odd: number[] = []
  const down441 = createDownsampler(44100, 16000)
  down441(new Float32Array(44100), s => odd.push(s))
  expect(Math.abs(odd.length - 16000)).toBeLessThanOrEqual(1)
})
