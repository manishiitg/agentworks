import { forwardRef, useCallback, useEffect, useImperativeHandle, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { Mic, Square, Loader2, Download } from 'lucide-react'
import { useVoiceDictation } from '../../shared/voice/useVoiceDictation'
import { getApiBaseUrl, getAuthToken } from '../services/api'
import type { VoiceEngineStatus } from '../services/api-types'
import { useCapabilitiesStore } from '../stores/useCapabilitiesStore'

/** Same vocabulary as SparkQuill's composer (learning-app/src/voice/useMicDictation.ts). */
export type MicState = 'idle' | 'preparing' | 'recording' | 'transcribing'

export type MicButtonHandle = {
  /** No-op unless currently recording: stops, waits for the committed
   *  transcript, and places it in the composer — what Enter does mid-dictation. */
  stopDictation: () => void
}

interface MicButtonProps {
  /** Product profile whose runtime.capabilities.voice gates the stream, or ''
   * from AgentWorks' own composer (gated by capabilities.voice.available). */
  profileId: string
  /** Fired ONCE per session, on stop, with the whole utterance. `autoSubmit`
   * is true only when autoSubmitOnStop() said so at that moment — AgentWorks
   * never passes autoSubmitOnStop, so it's always false there and the text
   * only ever lands in the composer for the user to review and send. */
  onText: (text: string, autoSubmit: boolean) => void
  /** Lets the composer know when Enter should stop+send instead of send. */
  onStateChange?: (state: MicState) => void
  /** Where the full-width live banner renders (a `relative` host above the
   * composer). Falls back to a bubble beside the button. */
  bannerHost?: HTMLElement | null
  /** Only the visible composer should own the global ⌥ / ⌘⇧M shortcuts. */
  shortcutEnabled?: boolean
  disabled?: boolean
  /** Called at the moment a recording stops; true sends the transcript right
   * away instead of leaving it for the user to send (SparkQuill's opt-in
   * voice auto-send). A function, not a plain boolean, so a Settings toggle
   * flipped mid-recording is read fresh at stop time, not from render-time
   * props. Omit to never auto-send. */
  autoSubmitOnStop?: () => boolean
}

type SetupPhase = 'unknown' | 'needed' | 'offer' | 'downloading' | 'ready' | 'failed'

/**
 * Push-to-talk mic for the chat composer — SparkQuill's mic, on the shared
 * AgentWorks engine (frontend/shared/voice/useVoiceDictation.ts →
 * /api/voice/stream → agent_go/pkg/voicestt), with the same UX:
 *
 * - click / ⌥ tap / ⌘⇧M to start; the button turns red with a level ring that
 *   scales with LIVE input, and a "Listening" banner above the composer shows
 *   the running transcript as the engine hears it (it may revise itself
 *   between updates, as any live captioning does);
 * - click / ⌥ tap / Enter while recording = stop; the committed text lands
 *   in the composer for the user to review and send. AgentWorks never
 *   passes autoSubmitOnStop, so it is always sent by hand there; SparkQuill
 *   passes it wired to its own opt-in Settings toggle;
 * - "Starting — getting voice ready…" covers opening the mic and a cold
 *   engine load, so an impatient re-click can't begin a second session;
 * - errors (nearly always a denied mic permission) show in place, dismiss on
 *   click.
 *
 * One addition SparkQuill puts in Settings instead: the engine's model is a
 * one-time ~690MB download on the server, so the first click offers that
 * explicitly and shows progress from GET /api/voice/status, rather than the
 * first click silently waiting on it (60+ seconds looking broken, caught live).
 *
 * Only mounted where the stream can work — see the call site in ChatInput.
 */
export const MicButton = forwardRef(function MicButton({
  profileId,
  onText,
  onStateChange,
  bannerHost,
  shortcutEnabled = true,
  disabled,
  autoSubmitOnStop,
}: MicButtonProps, ref: React.ForwardedRef<MicButtonHandle>) {
  const initialStatus = useCapabilitiesStore(state => state.capabilities?.voice)
  // The capabilities snapshot is fetched once at app start. If the engine was still loading then,
  // it says "loading" for the rest of the session, and every remount (each workflow or Crew switch)
  // put the loading bar back up until the first poll. An in-flight snapshot is only a hint: start
  // unknown and ask the server below.
  const [setup, setSetup] = useState<SetupPhase>(() => {
    const phase = phaseFor(initialStatus)
    return phase === 'downloading' ? 'unknown' : phase
  })
  const [engine, setEngine] = useState<VoiceEngineStatus | undefined>(initialStatus)
  const [setupError, setSetupError] = useState<string | null>(null)

  // /api/voice/stream on this agent server. Auth rides in the query because
  // a browser cannot set a custom header on a WebSocket upgrade; profile_id
  // (when set) is what the server gates the product's capability on.
  const streamUrl = useCallback(() => {
    const params = new URLSearchParams()
    if (profileId) params.set('profile_id', profileId)
    const token = getAuthToken()
    if (token) params.set('token', token)
    // Same-origin deployments (the EC2 gateway) leave the API base empty; a
    // WebSocket needs an absolute URL, so fall back to the page origin like
    // getTerminalStreamUrl does. http(s) -> ws(s).
    const httpBase = getApiBaseUrl() || (typeof window !== 'undefined' ? window.location.origin : '')
    return `${httpBase.replace(/^http/i, 'ws')}/api/voice/stream?${params.toString()}`
  }, [profileId])
  const { state, error, level, transcript, start, stop, clearError } = useVoiceDictation({ streamUrl })

  const micState: MicState =
    state === 'starting' ? 'preparing'
      : state === 'listening' ? 'recording'
        : state === 'finishing' ? 'transcribing'
          : 'idle'
  const micStateRef = useRef<MicState>(micState)
  micStateRef.current = micState
  useEffect(() => { onStateChange?.(micState) }, [micState, onStateChange])

  const authHeaders = useCallback((): HeadersInit | undefined => {
    const token = getAuthToken()
    return token ? { Authorization: `Bearer ${token}` } : undefined
  }, [])

  const refreshStatus = useCallback(async (): Promise<VoiceEngineStatus | undefined> => {
    try {
      const res = await fetch(`${getApiBaseUrl()}/api/voice/status`, { headers: authHeaders() })
      if (!res.ok) return undefined
      const status = (await res.json()) as VoiceEngineStatus
      setEngine(status)
      setSetup(phaseFor(status))
      // Keep the shared snapshot current so the next mount starts from the truth.
      useCapabilitiesStore.setState(state => state.capabilities ? { capabilities: { ...state.capabilities, voice: status } } : {})
      return status
    } catch {
      return undefined
    }
  }, [authHeaders])

  // A snapshot that said "in flight" at app start: check once instead of showing the bar.
  const staleInFlight = useRef(phaseFor(initialStatus) === 'downloading')
  useEffect(() => {
    if (!staleInFlight.current) return
    staleInFlight.current = false
    void refreshStatus()
  }, [refreshStatus])

  // Poll only while a download or load is in flight — the progress bar is
  // the only reason to hit the server repeatedly.
  useEffect(() => {
    if (setup !== 'downloading') return
    let misses = 0
    const id = window.setInterval(() => {
      void refreshStatus().then((status) => {
        if (status) {
          misses = 0
          // While setting up, only "ready" or an error ends the banner: a
          // snapshot that merely hasn't caught up yet must not dismiss it.
          if (!status.ready && !status.error) setSetup('downloading')
          return
        }
        // Ten seconds without an answer: the server went away mid-download
        // (seen live). Say so rather than showing a frozen spinner.
        if (++misses >= 10) {
          setSetup('failed')
          setSetupError('The server stopped answering during the download. Once it is back, click the mic to resume.')
        }
      })
    }, 1000)
    return () => window.clearInterval(id)
  }, [setup, refreshStatus])

  const beginSetup = useCallback(async () => {
    setSetup('downloading')
    try {
      const params = new URLSearchParams()
      if (profileId) params.set('profile_id', profileId)
      const res = await fetch(`${getApiBaseUrl()}/api/voice/warm?${params.toString()}`, { method: 'POST', headers: authHeaders() })
      if (!res.ok) { setSetup('failed'); return }
      const status = (await res.json()) as VoiceEngineStatus
      setEngine(status)
      // The server has just been ASKED to download; keep the progress banner
      // up regardless of what this first snapshot says. Polling below moves
      // to ready/failed when the server actually gets there.
      setSetup(status.ready ? 'ready' : status.error ? 'failed' : 'downloading')
    } catch {
      setSetup('failed')
    }
  }, [profileId, authHeaders])

  const finish = useCallback(async () => {
    const text = (await stop()).trim()
    if (text) onText(text, autoSubmitOnStop?.() ?? false)
  }, [stop, onText, autoSubmitOnStop])

  const begin = useCallback(async () => {
    setSetupError(null)
    if (setup === 'ready') { void start(); return }
    if (setup === 'downloading') return
    if (setup === 'offer') { void beginSetup(); return }
    // 'unknown' / 'needed' / 'failed': ask the server what the truth is now,
    // then either start or put the offer up.
    const status = await refreshStatus()
    if (status?.ready) { void start(); return }
    if (status && (status.downloading || status.loading)) { setSetup('downloading'); return }
    if (status && !status.installed) { setSetup('offer'); return }
    if (status?.installed && !status.ready) {
      // On disk but cold: warm and start; the stream waits for the load.
      void fetch(`${getApiBaseUrl()}/api/voice/warm?${profileId ? `profile_id=${encodeURIComponent(profileId)}` : ''}`, { method: 'POST', headers: authHeaders() }).catch(() => {})
      void start()
      return
    }
    setSetupError('Voice isn’t reachable right now.')
  }, [setup, start, beginSetup, refreshStatus, profileId, authHeaders])

  const toggle = useCallback(() => {
    if (micStateRef.current === 'recording') void finish()
    else if (micStateRef.current === 'idle') void begin()
  }, [finish, begin])

  const stopDictation = useCallback(() => {
    if (micStateRef.current !== 'recording') return
    void finish()
  }, [finish])
  useImperativeHandle(ref, () => ({ stopDictation }), [stopDictation])

  // ⌘⇧M (Ctrl⇧M) toggles dictation from anywhere in the window.
  useEffect(() => {
    if (!shortcutEnabled || disabled) return
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.shiftKey && (e.key === 'm' || e.key === 'M')) {
        e.preventDefault()
        toggle()
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [shortcutEnabled, disabled, toggle])

  // A solo tap of ⌥ (Option) toggles too — the one-finger shortcut. A held or
  // combined ⌥ (⌥+arrow, ⌥+letter) is left alone.
  useEffect(() => {
    if (!shortcutEnabled || disabled) return
    let downAt = 0
    let soloTap = false
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Alt') {
        if (e.repeat) return
        downAt = Date.now()
        soloTap = true
        return
      }
      soloTap = false
    }
    const onKeyUp = (e: KeyboardEvent) => {
      if (e.key !== 'Alt') return
      const wasTap = soloTap && Date.now() - downAt < 500
      soloTap = false
      if (wasTap) toggle()
    }
    const cancel = () => { soloTap = false }
    window.addEventListener('keydown', onKeyDown)
    window.addEventListener('keyup', onKeyUp)
    window.addEventListener('mousedown', cancel)
    window.addEventListener('blur', cancel)
    return () => {
      window.removeEventListener('keydown', onKeyDown)
      window.removeEventListener('keyup', onKeyUp)
      window.removeEventListener('mousedown', cancel)
      window.removeEventListener('blur', cancel)
    }
  }, [shortcutEnabled, disabled, toggle])

  const stopOrStart = () => toggle()

  const preparing = micState === 'preparing'
  const recording = micState === 'recording'
  const busy = micState === 'transcribing' || preparing || setup === 'downloading'
  const pct = engine && engine.total_bytes > 0 ? Math.min(100, Math.round((engine.got_bytes / engine.total_bytes) * 100)) : 0
  const sizeLabel = `${engine?.size_mb ?? 690}MB`
  const shownError = setupError ?? error
  const transcriptPreview = latestTranscriptPreview(transcript)

  const label = recording
    ? 'Stop dictation (tap ⌥)'
    : preparing
      ? 'Getting voice ready…'
      : micState === 'transcribing'
        ? 'Transcribing…'
        : setup === 'downloading'
          ? (engine?.downloading ? `Downloading the voice model… ${pct}%` : 'Loading the voice model…')
          : setup === 'offer'
            ? `Set up voice (one-time ${sizeLabel} download)`
            : 'Speak your message (tap ⌥, or ⌘⇧M)'

  // Arbitrary-value colors, not `bg-slate-900`/`dark:bg-slate-950`: the VS Code
  // theme retrofit (src/index.css, src/themes/vscode-*.css) remaps every
  // hardcoded slate-900 utility to the active theme's `--background` with
  // `!important`, so it can't stay literally dark. That's invisible in
  // AgentWorks (its theme is forced dark), but this banner is meant to read as
  // a dark caption box on ANY page — including a product surface's own light
  // theme, where the remap turned it into white text on a near-white pill.
  const bannerClass = 'flex items-start gap-2.5 rounded-2xl bg-[#0f172a] px-4 py-3 text-sm text-white shadow-[0_14px_32px_rgba(13,35,76,0.3)] dark:bg-[#020617] dark:ring-1 dark:ring-slate-700'
  const bannerLabelClass = 'shrink-0 text-[11px] font-extrabold uppercase tracking-wider text-amber-300'
  let banner: React.ReactNode = null
  if (preparing) {
    banner = (
      <span className={bannerClass} role="status">
        <Loader2 className="h-4 w-4 shrink-0 animate-spin" aria-hidden="true" />
        <span className={bannerLabelClass}>Starting</span>
        <span className="min-w-0 flex-1 italic text-slate-300">Getting voice ready — this can take a moment the first time…</span>
      </span>
    )
  } else if (recording || micState === 'transcribing') {
    banner = (
      <span className={bannerClass} role="status">
        <span className="h-2.5 w-2.5 shrink-0 animate-pulse rounded-full bg-red-500" aria-hidden="true" />
        <span className="sr-only">{recording ? 'Listening' : 'Finishing'}</span>
        <span
          className={`min-w-0 flex-1 whitespace-pre-wrap break-words leading-6 ${transcriptPreview ? '' : 'italic text-slate-300'}`}
          title={transcript || undefined}
        >
          {transcriptPreview || 'Go ahead — start talking'}
        </span>
      </span>
    )
  } else if (setup === 'downloading') {
    const gotMB = Math.round((engine?.got_bytes ?? 0) / (1024 * 1024))
    const totalMB = Math.round((engine?.total_bytes ?? 0) / (1024 * 1024)) || (engine?.size_mb ?? 690)
    banner = (
      <span className={bannerClass} role="status">
        <Loader2 className="h-4 w-4 shrink-0 animate-spin" aria-hidden="true" />
        <span className={bannerLabelClass}>Setting up</span>
        <span className="min-w-0 flex-1">
          {engine?.downloading
            ? `Downloading the voice model: ${gotMB} of ${totalMB}MB (${pct}%). One time only — you can talk as soon as it finishes.`
            : 'Loading the voice model… almost there.'}
        </span>
        <span className="relative h-2 w-36 shrink-0 overflow-hidden rounded-full bg-slate-700" aria-hidden="true">
          <span className="absolute inset-y-0 left-0 rounded-full bg-amber-400 transition-[width] duration-500" style={{ width: `${engine?.downloading ? pct : 100}%` }} />
        </span>
      </span>
    )
  } else if (setup === 'offer') {
    banner = (
      <span className={bannerClass} role="status">
        <Download className="h-4 w-4 shrink-0" aria-hidden="true" />
        <span className={bannerLabelClass}>Voice</span>
        <span className="min-w-0 flex-1">Talking instead of typing needs a one-time {sizeLabel} download on the server. It runs there, not in the cloud.</span>
        <button type="button" className="shrink-0 rounded-full bg-amber-400 px-3 py-1 text-xs font-bold text-slate-900 hover:bg-amber-300" onClick={() => { void beginSetup() }}>
          Download
        </button>
        <button type="button" className="shrink-0 text-slate-400 hover:text-white" onClick={() => setSetup('needed')} aria-label="Not now">✕</button>
      </span>
    )
  }

  const bannerNode = banner && (
    bannerHost
      ? createPortal(<div className="mb-2">{banner}</div>, bannerHost)
      : <span className="absolute left-full top-1/2 z-10 ml-2 w-max max-w-[420px] -translate-y-1/2">{banner}</span>
  )

  return (
    <span className="relative inline-flex items-center">
      <button
        type="button"
        onClick={stopOrStart}
        disabled={disabled || busy}
        title={label}
        aria-label={label}
        data-testid="chat-input-mic-button"
        data-voice-state={micState}
        data-voice-setup={setup}
        className={`relative inline-flex h-7 w-7 items-center justify-center rounded-md transition-colors ${
          recording
            ? 'bg-red-100 text-red-700 ring-1 ring-red-300 dark:bg-red-500/15 dark:text-red-400 dark:ring-red-500/40'
            : 'border border-input bg-background text-muted-foreground shadow-sm hover:bg-accent hover:text-accent-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring'
        } ${disabled ? 'opacity-50 cursor-not-allowed' : ''}`}
      >
        {busy
          ? <Loader2 className="h-4 w-4 animate-spin" />
          : recording
            ? <Square className="h-3.5 w-3.5" />
            : setup === 'offer'
              ? <Download className="h-4 w-4" />
              : <Mic className="h-4 w-4" />}
        {recording && (
          // Scales with LIVE input level, not a fixed pulse — a silent or
          // wrong input device shows a dead ring instead of silently
          // recording nothing.
          <span
            aria-hidden="true"
            className="pointer-events-none absolute -inset-[3px] rounded-full border-2 border-red-400/40 transition-transform duration-75"
            style={{ transform: `scale(${1 + level * 0.7})` }}
          />
        )}
      </button>
      {setup === 'downloading' && engine?.downloading && (
        <span className="ml-1 text-[11px] tabular-nums text-slate-500 dark:text-slate-400" aria-hidden="true">{pct}%</span>
      )}
      {bannerNode}
      {shownError && !busy && !recording && (
        <span
          role="status"
          className="absolute bottom-[calc(100%+8px)] left-0 z-20 w-max max-w-[260px] cursor-pointer rounded-xl bg-slate-900 px-2.5 py-1.5 text-xs leading-snug text-white shadow-lg dark:bg-slate-950 dark:ring-1 dark:ring-slate-700"
          onClick={() => { setSetupError(null); clearError() }}
          title="Dismiss"
        >
          {friendlyError(shownError)}
        </span>
      )}
    </span>
  )
})

function friendlyError(message: string): string {
  if (/not reachable|failed to connect/i.test(message)) return 'Voice isn’t available right now — the server didn’t answer.'
  if (/Permission|NotAllowed|denied/i.test(message)) return 'Could not use the microphone. Check permission for this app.'
  return message
}

// Live dictation can grow for several minutes. Keep the newest wording—the
// part the speaker is actively refining—rather than clipping the newest text
// at the bottom of the banner. The full transcript still lands in the composer
// when dictation stops.
function latestTranscriptPreview(transcript: string, maxChars = 420): string {
  if (transcript.length <= maxChars) return transcript
  const tail = transcript.slice(-maxChars)
  const firstWordBoundary = tail.search(/\s/)
  return `…${(firstWordBoundary >= 0 ? tail.slice(firstWordBoundary) : tail).trimStart()}`
}

function phaseFor(status: VoiceEngineStatus | undefined): SetupPhase {
  if (!status) return 'unknown'
  if (status.ready) return 'ready'
  if (status.downloading || status.loading) return 'downloading'
  if (status.error) return 'failed'
  return 'needed'
}
