/**
 * Raw-PCM microphone capture at 16kHz mono, matching the sample rate
 * pkg/voicestt.SampleRate expects server-side. Shared by every microphone
 * consumer so resampling and chunk boundaries stay consistent.
 *
 * The AudioContext runs at the device's own rate (44.1/48kHz). Asking the
 * browser for a 16kHz context made it deliver only about a third of the audio
 * on a live server, so the speech engine heard nothing it could transcribe;
 * capture now averages down to 16kHz here.
 */

/** 160ms at 16kHz — small enough for responsive partials, large enough to
 * not spam the websocket with a message per render quantum. */
export const PCM_CHUNK_SAMPLES = 2560
export const PCM_SAMPLE_RATE = 16000

// Registered from a Blob URL rather than a served file: an AudioWorklet module
// must be fetched by URL, and inlining it is immune to asset-path differences
// between dev and packaged builds.
const WORKLET_SOURCE = `
class PcmCollector extends AudioWorkletProcessor {
  process(inputs) {
    const channel = inputs[0] && inputs[0][0]
    // A copy is required: the render quantum's buffer is reused by the audio
    // thread the moment this returns, so posting it directly would deliver
    // whatever the next quantum overwrote it with.
    if (channel) this.port.postMessage(new Float32Array(channel))
    return true
  }
}
registerProcessor('pcm-collector', PcmCollector)
`

export type PcmCapture = { stop: () => void }

// Contexts whose worklet module is already registered, so a capture started
// after ensurePcmWorklet() does not pay the module load twice.
const registered = new WeakSet<AudioContext>()

/** Registers the PCM worklet on `ctx`. Safe to call early and concurrently
 *  with the microphone prompt — that overlap is where the visible startup
 *  delay used to go (measured: ~3s from socket open to first chunk). */
export async function ensurePcmWorklet(ctx: AudioContext): Promise<void> {
  if (registered.has(ctx)) return
  const url = URL.createObjectURL(new Blob([WORKLET_SOURCE], { type: 'application/javascript' }))
  try {
    await ctx.audioWorklet.addModule(url)
    registered.add(ctx)
  } finally {
    URL.revokeObjectURL(url)
  }
}

/**
 * Streams `stream`'s audio to `onChunk` as PCM16 little-endian bytes, in
 * PCM_CHUNK_SAMPLES-sample slices. The caller owns both the AudioContext and
 * the MediaStream (and is responsible for closing/stopping them).
 */
export async function startPcmCapture(
  ctx: AudioContext,
  stream: MediaStream,
  onChunk: (pcm16: ArrayBuffer) => void,
): Promise<PcmCapture> {
  await ensurePcmWorklet(ctx)
  if (ctx.state === 'suspended') {
    // Created outside a user gesture, or the tab was backgrounded: without
    // this the worklet never runs and nothing is ever sent.
    await ctx.resume()
  }

  const source = ctx.createMediaStreamSource(stream)
  const node = new AudioWorkletNode(ctx, 'pcm-collector')

  // The worklet emits one render quantum (128 frames, ~8ms) at a time — far
  // too small to be worth a websocket send each. Coalesce up to a real chunk
  // first.
  let pending: number[] = []
  const toTarget = createDownsampler(ctx.sampleRate, PCM_SAMPLE_RATE)
  node.port.onmessage = (event: MessageEvent<Float32Array>) => {
    toTarget(event.data, sample => pending.push(sample))
    while (pending.length >= PCM_CHUNK_SAMPLES) {
      const slice = pending.splice(0, PCM_CHUNK_SAMPLES)
      onChunk(floatTo16BitPCM(slice))
    }
  }

  source.connect(node)
  // AudioWorkletNode must be connected to a destination for its process()
  // callback to run in some browsers, even though this capture path never
  // wants to play the audio back. muted via gain 0 rather than connecting
  // straight to ctx.destination, which would echo the mic to the speakers.
  const silence = ctx.createGain()
  silence.gain.value = 0
  node.connect(silence)
  silence.connect(ctx.destination)

  return {
    stop: () => {
      node.port.onmessage = null
      try { source.disconnect() } catch { /* already disconnected */ }
      try { node.disconnect() } catch { /* already disconnected */ }
      try { silence.disconnect() } catch { /* already disconnected */ }
    },
  }
}

/** Converts a stream at `from` Hz to `to` Hz by averaging each output sample's
 *  window of input samples (a box filter, enough to keep speech intelligible).
 *  State carries across calls, so chunk boundaries add no clicks. */
export function createDownsampler(from: number, to: number) {
  const step = from / to
  let sum = 0, count = 0, position = 0
  return (input: ArrayLike<number>, emit: (sample: number) => void) => {
    if (step <= 1) { for (let i = 0; i < input.length; i++) emit(input[i]); return }
    for (let i = 0; i < input.length; i++) {
      sum += input[i]; count++; position++
      if (position >= step) {
        emit(sum / count)
        position -= step; sum = 0; count = 0
      }
    }
  }
}

function floatTo16BitPCM(samples: number[]): ArrayBuffer {
  const buffer = new ArrayBuffer(samples.length * 2)
  const view = new DataView(buffer)
  for (let i = 0; i < samples.length; i++) {
    const clamped = Math.max(-1, Math.min(1, samples[i]))
    const int16 = clamped < 0 ? clamped * 0x8000 : clamped * 0x7fff
    view.setInt16(i * 2, int16, true) // little-endian, matches decodePCM16 server-side
  }
  return buffer
}
