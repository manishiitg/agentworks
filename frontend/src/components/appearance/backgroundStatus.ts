import { useAppearanceStore, type AppearanceBackgroundStatus } from '../../stores/useAppearanceStore'

type Kind = AppearanceBackgroundStatus['kind']
type State = AppearanceBackgroundStatus['state']

/** Publishes one background's state and measured frame rate for the appearance report.
 * Small frame-rate changes are not news: the report is re-sent only on real changes. */
export function backgroundStatus(kind: Kind) {
  let current: AppearanceBackgroundStatus = { kind, state: 'stopped' }
  const publish = (next: AppearanceBackgroundStatus) => {
    current = next
    useAppearanceStore.getState().setBackground(next)
  }
  return {
    state: (state: State, error?: string) => publish({ kind, state, ...(current.fps !== undefined && state !== 'error' && state !== 'stopped' ? { fps: current.fps } : {}), ...(error ? { error: error.slice(0, 300) } : {}) }),
    fps: (fps: number) => {
      if (current.fps === undefined || Math.abs(fps - current.fps) >= 3) publish({ ...current, fps })
    },
    clear: () => {
      if (useAppearanceStore.getState().background?.kind === kind) useAppearanceStore.getState().setBackground(null)
    },
  }
}
