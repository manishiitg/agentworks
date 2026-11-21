import { useEffect, useRef, useState } from 'react'
import { shownTheme, useAppearanceStore } from '../../stores/useAppearanceStore'
import { useAuthStore } from '../../stores/useAuthStore'
import api from '../../services/api'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'
import { backgroundStatus } from './backgroundStatus'

/** One passive decoder, not a theme renderer or script. Remount on account/server change. */
export function AppearanceVideo() {
  const person = useAuthStore(state => state.user?.id)
  const server = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  const scope = useAppearanceStore(state => state.scope)
  const video = useAppearanceStore(state => shownTheme(state).background_video)
  if (!person || scope !== JSON.stringify([server, person]) || !video) return null
  return <PersonalVideo key={JSON.stringify([person, server, video.url, video.asset_id])} video={video} />
}
function PersonalVideo({ video }: { video: { url?: string; asset_id?: string; dim: number; playback_rate: number } }) {
  const [source, setSource] = useState(video.url ?? '')
  useEffect(() => {
    if (!video.asset_id) return
    const controller = new AbortController()
    let ownedURL: string | undefined
    void api.get<Blob>(`/api/appearance/videos/${video.asset_id}`, { responseType: 'blob', skipSessionContext: true, signal: controller.signal }).then(({ data }) => {
      if (controller.signal.aborted || data.size > 24 * 1024 * 1024 || !data.type.startsWith('video/mp4')) return
      ownedURL = URL.createObjectURL(data)
      setSource(ownedURL)
    }).catch(() => { /* Palette/CSS fallback; no credential in a media URL. */ })
    return () => { controller.abort(); if (ownedURL) URL.revokeObjectURL(ownedURL) }
  }, [video.asset_id])
  return source ? <VideoLayer url={source} dim={video.dim} playback_rate={video.playback_rate} /> : null
}
function VideoLayer({ url, dim, playback_rate }: { url: string; dim: number; playback_rate: number }) {
  const ref = useRef<HTMLVideoElement>(null)
  const [failed, setFailed] = useState(false)
  useEffect(() => {
    const report = backgroundStatus('video')
    if (failed) {
      report.state('error', 'The video could not be played.')
      return () => report.clear()
    }
    const video = ref.current
    if (!video) return
    const motion = matchMedia('(prefers-reduced-motion: reduce)')
    let disposed = false
    const playing = () => report.state('running')
    const paused = () => report.state('paused')
    video.src = url
    video.muted = true
    video.playbackRate = playback_rate
    const ready = () => { document.documentElement.dataset.appearanceVideo = ''; update() }
    const update = () => {
      if (document.hidden || motion.matches) video.pause()
      else void video.play().catch(error => { if (!disposed && error?.name !== 'AbortError') setFailed(true) })
    }
    update()
    video.addEventListener('loadeddata', ready)
    video.addEventListener('playing', playing)
    video.addEventListener('pause', paused)
    document.addEventListener('visibilitychange', update)
    motion.addEventListener('change', update)
    return () => {
      disposed = true
      delete document.documentElement.dataset.appearanceVideo
      video.pause()
      video.removeEventListener('loadeddata', ready)
      video.removeEventListener('playing', playing)
      video.removeEventListener('pause', paused)
      report.clear()
      document.removeEventListener('visibilitychange', update)
      motion.removeEventListener('change', update)
      video.removeAttribute('src')
      video.load()
    }
  }, [url, playback_rate, failed])
  if (failed) return null // Keep the person's static CSS/palette fallback.
  return <div aria-hidden data-theme-video className="pointer-events-none fixed inset-0" style={{ zIndex: 0 }}>
    <video ref={ref} src={url} muted loop playsInline preload="metadata" disablePictureInPicture tabIndex={-1} onError={() => setFailed(true)} className="h-full w-full object-cover" />
    <div className="absolute inset-0" style={{ backgroundColor: `rgba(0,0,0,${dim})` }} />
  </div>
}
