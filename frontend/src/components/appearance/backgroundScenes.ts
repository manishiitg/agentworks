export type BackgroundScene = 'none' | 'neon-orbit' | 'deep-space'

/** Only shipped, passive renderers. CSS never supplies executable scene code. */
export function backgroundScene(value: string, profile: 'light' | 'dark'): BackgroundScene {
  if (profile !== 'dark') return 'none'
  const scene = value.trim()
  return scene === 'neon-orbit' || scene === 'deep-space' ? scene : 'none'
}
