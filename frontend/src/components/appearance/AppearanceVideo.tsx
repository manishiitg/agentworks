import { useEffect, useRef, useState } from 'react'
import { useAppearanceStore } from '../../stores/useAppearanceStore'
import { useAuthStore } from '../../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'

/** One passive decoder, not a theme renderer or script. Remount on account/server change. */
export function AppearanceVideo() {
  const person = useAuthStore(state => state.user?.id)
  const server = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  const scope = useAppearanceStore(state => state.scope)
  const video = useAppearanceStore(state => (state.preview ?? state.current).background_video)
  if (!person || scope !== JSON.stringify([server, person]) || !video) return null
  return <VideoLayer key={JSON.stringify([person, server, video.url])} {...video} />
}
function VideoLayer({ url, dim, playback_rate }: { url: string; dim: number; playback_rate: number }) {
  const ref = useRef<HTMLVideoElement>(null)
  const [failed, setFailed] = useState(false)
  useEffect(() => {
    const video = ref.current
    if (!video) return
    const motion = matchMedia('(prefers-reduced-motion: reduce)')
    let disposed = false
    if (failed) return
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
    document.addEventListener('visibilitychange', update)
    motion.addEventListener('change', update)
    return () => {
      disposed = true
      delete document.documentElement.dataset.appearanceVideo
      video.pause()
      video.removeEventListener('loadeddata', ready)
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
