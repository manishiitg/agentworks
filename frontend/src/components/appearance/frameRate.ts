const WINDOW_MS = 4000

/** Measures frames actually drawn over a few seconds, for the appearance report. */
export function frameRateMeter(onFps?: (fps: number) => void) {
  let start = 0
  let frames = 0
  return {
    frame: (now: number) => {
      if (!onFps) return
      if (!start) {
        start = now
        frames = 0
        return
      }
      frames++
      if (now - start < WINDOW_MS) return
      onFps(Math.round((frames * 1000) / (now - start)))
      start = now
      frames = 0
    },
    reset: () => {
      start = 0
      frames = 0
    },
  }
}
