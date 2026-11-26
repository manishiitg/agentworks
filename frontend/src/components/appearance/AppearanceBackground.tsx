import { useEffect, useRef, useState } from 'react'
import { shownMode, shownTheme, useAppearanceStore } from '../../stores/useAppearanceStore'
import { useAuthStore } from '../../stores/useAuthStore'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'
import { useChatStore } from '../../stores/useChatStore'
import { chatRuntimeActivity } from '../../utils/chatRuntimeActivity'
import { hasActiveSessionWork } from '../../utils/activitySessions'
import { backgroundActiveInMode, type AppearanceShader } from '../../stores/appearanceTheme'
import { backgroundStatus } from './backgroundStatus'
import { PersonalScript } from './PersonalScript'
import { mountAppearanceShader } from './shaderEngine'

type RGB = [number, number, number]

/** Whether the open chat is running: the one input a background gets besides colours. */
function useActiveChatRunning() {
  return useChatStore(state => {
    const tab = state.activeTabId ? state.chatTabs[state.activeTabId] : undefined
    if (!tab) return false
    return chatRuntimeActivity(tab, state.activeSessionsCache.find(row => row.session_id === tab.sessionId)).state === 'running'
  })
}

/** Whether any admitted agent or session in the sidebar is currently running. */
function useAnyAgentRunning() {
  return useChatStore(state =>
    state.activeSessionsCache.some(hasActiveSessionWork) ||
    Object.values(state.chatTabs).some(tab => chatRuntimeActivity(tab, state.activeSessionsCache.find(row => row.session_id === tab.sessionId)).state === 'running'),
  )
}

/** Personal script or shader background, behind every app surface. */
export default function AppearanceBackground() {
  const actor = useAuthStore(state => state.user?.id)
  const server = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  const scope = useAppearanceStore(state => state.scope)
  const theme = useAppearanceStore(shownTheme)
  const profile = useAppearanceStore(shownMode)
  const working = useActiveChatRunning()
  const anyWorking = useAnyAgentRunning()
  if (!actor || scope !== JSON.stringify([server, actor])) return null
  const colors = theme.colors[profile] ?? {}
  const scriptActive = !!theme.background_script && backgroundActiveInMode(theme.background_script.mode, profile)
  const shaderActive = !!theme.background_shader && backgroundActiveInMode(theme.background_shader.mode, profile)
  return <>
    {scriptActive && theme.background_script && <PersonalScript key={JSON.stringify([actor, server, theme.background_script])} script={theme.background_script} colors={colors} working={working} anyWorking={anyWorking} />}
    {shaderActive && theme.background_shader && <PersonalShader key={JSON.stringify([actor, server, theme.background_shader])} shader={theme.background_shader} accent={colors.accent} working={working} anyWorking={anyWorking} />}
  </>
}

const hexToRGB = (hex: string): RGB => [1, 3, 5].map(i => parseInt(hex.slice(i, i + 2), 16) / 255) as RGB

/** Without a theme accent, use the app's own primary token ("H S% L%"). */
function primaryTokenRGB(): RGB {
  const [h, s, l] = getComputedStyle(document.documentElement).getPropertyValue('--primary').trim().split(/\s+/).map(part => parseFloat(part))
  if (![h, s, l].every(Number.isFinite)) return [.3, .7, 1]
  const saturation = s / 100, lightness = l / 100
  const k = (n: number) => (n + h / 30) % 12
  const a = saturation * Math.min(lightness, 1 - lightness)
  const channel = (n: number) => lightness - a * Math.max(-1, Math.min(k(n) - 3, 9 - k(n), 1))
  return [channel(0), channel(8), channel(4)]
}

function PersonalShader({ shader, accent, working, anyWorking }: { shader: AppearanceShader; accent?: string; working: boolean; anyWorking: boolean }) {
  const canvas = useRef<HTMLCanvasElement>(null)
  const engine = useRef<ReturnType<typeof mountAppearanceShader>>(null)
  const [ready, setReady] = useState(false)
  useEffect(() => {
    const report = backgroundStatus('shader')
    const mounted = mountAppearanceShader(canvas.current!, shader, (state, ms) => {
      if (state === 'ready') {
        setReady(true)
        document.documentElement.dataset.appearanceShader = ''
        report.state('running')
      }
      if (state === 'stopped' || state === 'failed') {
        setReady(false)
        delete document.documentElement.dataset.appearanceShader
      }
      if (state === 'failed') report.state('error', 'The shader did not compile or the graphics context was lost.')
      if (state === 'stopped') report.state('stopped')
      if (state === 'throttled') report.state('throttled')
      if (state === 'stopped' || state === 'throttled') console.info(`[APPEARANCE_SHADER] outcome=${state} ms=${Math.round(ms ?? 0)}`)
    }, report.fps)
    engine.current = mounted
    return () => {
      mounted?.destroy()
      engine.current = null
      delete document.documentElement.dataset.appearanceShader
      report.clear()
    }
  }, [shader.code, shader.speed, shader.intensity])
  useEffect(() => {
    engine.current?.setUniforms(accent ? hexToRGB(accent) : primaryTokenRGB(), working, anyWorking)
  }, [accent, working, anyWorking])
  return <div data-theme-shader aria-hidden style={{ visibility: ready ? 'visible' : 'hidden' }}><canvas ref={canvas} /></div>
}
