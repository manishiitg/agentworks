import { useEffect, useState } from 'react'
import { shownTheme, useAppearanceStore } from '../../stores/useAppearanceStore'
import { useAuthStore } from '../../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'
import type { AppearanceImage as ImageSettings } from '../../stores/appearanceTheme'
import api from '../../services/api'
import { backgroundStatus } from './backgroundStatus'

const MAX_IMAGE_BYTES = 5 * 1024 * 1024

export function AppearanceImage() {
  const actor = useAuthStore(state => state.user?.id)
  const server = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  const scope = useAppearanceStore(state => state.scope)
  const image = useAppearanceStore(state => shownTheme(state).background_image)
  if (!actor || scope !== JSON.stringify([server, actor]) || !image) return null
  return <PersonalImage key={JSON.stringify([actor, server, image.asset_id])} image={image} />
}

function PersonalImage({ image }: { image: ImageSettings }) {
  const [url, setURL] = useState('')
  useEffect(() => {
    const controller = new AbortController()
    const report = backgroundStatus('image')
    const decoded = new Image()
    let owned: string | undefined
    void api.get<Blob>(`/api/appearance/images/${image.asset_id}`, { responseType: 'blob', skipSessionContext: true, signal: controller.signal }).then(({ data }) => {
      if (controller.signal.aborted) return
      if (data.size > MAX_IMAGE_BYTES || !['image/png', 'image/jpeg'].includes(data.type)) {
        report.state('error', 'The image is not a PNG or JPEG up to 5 MB.')
        return
      }
      owned = URL.createObjectURL(data)
      decoded.onload = () => {
        if (controller.signal.aborted) return
        setURL(owned!)
        document.documentElement.dataset.appearanceImage = ''
        report.state('running')
      }
      decoded.onerror = () => { if (!controller.signal.aborted) report.state('error', 'The image could not be decoded.') }
      decoded.src = owned
    }).catch(() => {
      // Preserve the palette/CSS fallback.
      if (!controller.signal.aborted) report.state('error', 'The image could not be loaded.')
    })
    return () => {
      controller.abort()
      decoded.onload = null
      decoded.onerror = null
      decoded.src = ''
      if (owned) URL.revokeObjectURL(owned)
      delete document.documentElement.dataset.appearanceImage
      report.clear()
    }
  }, [image.asset_id])
  if (!url) return null
  return <div
    data-theme-image
    aria-hidden
    className="pointer-events-none fixed inset-0"
    style={{
      zIndex: 0,
      backgroundImage: `url("${url}")`,
      backgroundSize: image.fit === 'tile' ? 'auto' : image.fit,
      backgroundRepeat: image.fit === 'tile' ? 'repeat' : 'no-repeat',
      backgroundPosition: `${image.position_x}% ${image.position_y}%`,
      filter: `brightness(${image.brightness})`,
    }}
  />
}
