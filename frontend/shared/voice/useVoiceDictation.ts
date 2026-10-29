import { useCallback, useRef, useState } from 'react'
import { ensurePcmWorklet, startPcmCapture, type PcmCapture } from './pcmCapture'

export type VoiceDictationState = 'idle' | 'starting' | 'listening' | 'finishing' | 'error'

type VoiceStreamMessage = {
  type: 'partial' | 'final' | 'error'
  text?: string
  end_of_utterance?: boolean
  error?: string
}

export type VoiceDictationOptions = {
  /** Full ws(s):// URL of a voicestt stream endpoint, auth and any profile
   *  query already applied. Called at start, so a rotated token is picked up. */
  streamUrl: () => string
  /** Every transcript update: partials as the engine revises them, and
   *  finals when its endpoint rule fires (a pause) or on stop. */
  onText?: (text: string, final: boolean) => void
  /** Fired once, synchronously, when a NEW session starts (before mic
   *  permission is requested) — the caller's cue to reset whatever baseline
   *  it diffs partials against. */
  onStart?: () => void
  /** How long stop() waits for the server's committed transcript. */
  finishTimeoutMs?: number
}

/**
 * Push-to-talk mic dictation against the shared AgentWorks speech engine
 * (agent_go/pkg/voicestt, served as /api/voice/stream by the agent server for
 * AgentWorks and every product on it). One hook for every composer.
 *
 * Audio: AudioWorklet PCM16 at 16kHz, sent as binary WebSocket frames.
 * Text: `liveText` is the in-flight partial; `transcript` is everything the
 * engine has committed so far plus that partial, so a caller that only wants
 * the whole utterance reads it once `stop()` resolves.
 *
 * `level` is a live 0..1 mic amplitude, polled from an AnalyserNode on the
 * SAME stream the recognizer receives — not a decorative animation. A silent
 * or wrong input device shows a flat level with real chunks still arriving,
 * which is the one signal that tells "recording but hearing nothing" apart
 * from "not recording at all" (a live mic test showed 66s of connected,
 * chunk-receiving silence with no way to tell from the UI alone).
 *
 * `stop()` asks the server to flush, WAITS for the committed transcript, and
 * only then closes — closing first (as an earlier version did) raced the
 * flush and could drop the last phrase.
 */
export function useVoiceDictation(options: VoiceDictationOptions) {
  const { streamUrl, onText, onStart, finishTimeoutMs = 4000 } = options
  const [state, setState] = useState<VoiceDictationState>('idle')
  const [error, setError] = useState<string | null>(null)
  const [level, setLevel] = useState(0)
  const [liveText, setLiveText] = useState('')
  const [transcript, setTranscript] = useState('')
  const wsRef = useRef<WebSocket | null>(null)
  const audioCtxRef = useRef<AudioContext | null>(null)
  const mediaStreamRef = useRef<MediaStream | null>(null)
  const captureRef = useRef<PcmCapture | null>(null)
  const analyserFrameRef = useRef<number | null>(null)
  const committedRef = useRef('')
  const finishWaiterRef = useRef<((text: string) => void) | null>(null)
  const stateRef = useRef<VoiceDictationState>('idle')
  const setStateBoth = useCallback((next: VoiceDictationState) => { stateRef.current = next; setState(next) }, [])

  const stopCapture = useCallback(() => {
    if (analyserFrameRef.current !== null) {
      cancelAnimationFrame(analyserFrameRef.current)
      analyserFrameRef.current = null
    }
    setLevel(0)
    captureRef.current?.stop()
    captureRef.current = null
    mediaStreamRef.current?.getTracks().forEach((track) => track.stop())
    mediaStreamRef.current = null
    if (audioCtxRef.current && audioCtxRef.current.state !== 'closed') {
      void audioCtxRef.current.close()
    }
    audioCtxRef.current = null
  }, [])

  const closeSocket = useCallback(() => {
    if (wsRef.current && wsRef.current.readyState <= WebSocket.OPEN) {
      wsRef.current.close()
    }
    wsRef.current = null
  }, [])

  const start = useCallback(async () => {
    if (stateRef.current === 'starting' || stateRef.current === 'listening' || stateRef.current === 'finishing') return
    onStart?.()
    setStateBoth('starting')
    setError(null)
    setLiveText('')
    setTranscript('')
    committedRef.current = ''
    try {
      // Everything that can start now, starts now, in parallel: the audio
      // context (created synchronously, inside the click's user activation so
      // it is not born suspended), the worklet module load, the microphone
      // prompt, and the socket. Serially these added up to ~3s of "Getting
      // voice ready" on every click; the slowest of them alone is well under
      // a second.
      // The device's own rate: a 16 kHz context made the browser drop audio (a third of the
      // samples arrived), so capture converts to 16 kHz itself (pcmCapture).
      const audioCtx = new AudioContext()
      audioCtxRef.current = audioCtx
      const workletReady = ensurePcmWorklet(audioCtx)

      const ws = new WebSocket(streamUrl())
      ws.binaryType = 'arraybuffer'
      wsRef.current = ws
      const socketOpen = new Promise<void>((resolve, reject) => {
        ws.onopen = () => resolve()
        ws.onerror = () => reject(new Error('voice service is not reachable'))
      })
      // If the mic prompt fails first, the socket's later rejection has no
      // awaiter; mark it observed so it cannot surface as an unhandled one.
      socketOpen.catch(() => {})

      const stream = await navigator.mediaDevices.getUserMedia({ audio: true })
      mediaStreamRef.current = stream
      await Promise.all([socketOpen, workletReady])

      ws.onmessage = (event) => {
        let msg: VoiceStreamMessage
        try {
          msg = JSON.parse(event.data as string)
        } catch {
          return
        }
        if (msg.type === 'error') {
          setError(msg.error || 'transcription error')
          return
        }
        const text = msg.text ?? ''
        if (msg.type === 'final') {
          committedRef.current = joinPhrases(committedRef.current, text)
          setLiveText('')
          setTranscript(committedRef.current)
          if (text) onText?.(text, true)
          if (finishWaiterRef.current) {
            const resolve = finishWaiterRef.current
            finishWaiterRef.current = null
            resolve(committedRef.current)
          }
          return
        }
        if (!text) return
        setLiveText(text)
        setTranscript(joinPhrases(committedRef.current, text))
        onText?.(text, false)
      }
      ws.onclose = () => {
        if (wsRef.current === ws) {
          wsRef.current = null
          if (finishWaiterRef.current) {
            const resolve = finishWaiterRef.current
            finishWaiterRef.current = null
            resolve(committedRef.current)
          }
          setStateBoth('idle')
        }
      }

      // Peak deviation from silence, normalized — cheap and responsive for a
      // simple "is sound arriving" indicator.
      const analyser = audioCtx.createAnalyser()
      analyser.fftSize = 512
      audioCtx.createMediaStreamSource(stream).connect(analyser)
      const timeDomain = new Uint8Array(analyser.frequencyBinCount)
      const tickLevel = () => {
        analyser.getByteTimeDomainData(timeDomain)
        let peak = 0
        for (const v of timeDomain) peak = Math.max(peak, Math.abs(v - 128))
        setLevel(Math.min(1, peak / 96))
        analyserFrameRef.current = requestAnimationFrame(tickLevel)
      }
      tickLevel()

      captureRef.current = await startPcmCapture(audioCtx, stream, (pcm16) => {
        if (ws.readyState === WebSocket.OPEN) ws.send(pcm16)
      })

      setStateBoth('listening')
    } catch (err) {
      stopCapture()
      closeSocket()
      setError(err instanceof Error ? err.message : 'microphone access failed')
      setStateBoth('error')
    }
  }, [streamUrl, onText, onStart, stopCapture, closeSocket, setStateBoth])

  /** Stops the mic, flushes the server, and resolves with the whole committed
   *  transcript of this session. */
  const stop = useCallback((): Promise<string> => {
    stopCapture()
    const ws = wsRef.current
    if (!ws || ws.readyState !== WebSocket.OPEN) {
      closeSocket()
      setStateBoth('idle')
      return Promise.resolve(committedRef.current)
    }
    setStateBoth('finishing')
    return new Promise<string>((resolve) => {
      const timer = window.setTimeout(() => {
        if (finishWaiterRef.current) {
          finishWaiterRef.current = null
          resolve(committedRef.current)
        }
      }, finishTimeoutMs)
      finishWaiterRef.current = (text) => {
        window.clearTimeout(timer)
        resolve(text)
      }
      ws.send(JSON.stringify({ action: 'finish' }))
    }).finally(() => {
      closeSocket()
      setLiveText('')
      setStateBoth('idle')
    })
  }, [stopCapture, closeSocket, finishTimeoutMs, setStateBoth])

  const clearError = useCallback(() => {
    setError(null)
    if (stateRef.current === 'error') setStateBoth('idle')
  }, [setStateBoth])

  return { state, error, level, liveText, transcript, start, stop, clearError }
}

function joinPhrases(base: string, text: string): string {
  if (!base) return text
  if (!text) return base
  return `${base} ${text}`
}
