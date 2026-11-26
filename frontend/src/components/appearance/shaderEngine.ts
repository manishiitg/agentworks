import type { AppearanceShader } from '../../stores/appearanceTheme'
import { frameRateMeter } from './frameRate'

type Status = 'ready' | 'failed' | 'throttled' | 'stopped'
type TimerExtension = {
  TIME_ELAPSED_EXT: number
  QUERY_RESULT_AVAILABLE_EXT: number
  QUERY_RESULT_EXT: number
  GPU_DISJOINT_EXT: number
  createQueryEXT: () => object
  beginQueryEXT: (kind: number, query: object) => void
  endQueryEXT: (kind: number) => void
  getQueryObjectEXT: (query: object, key: number) => number | boolean
  deleteQueryEXT: (query: object) => void
}

const vertex = 'attribute vec2 a_position;void main(){gl_Position=vec4(a_position,0.,1.);}'
export const shaderFragment = (code: string) => `precision mediump float;
uniform float u_time;uniform vec2 u_resolution;uniform vec3 u_accent;uniform float u_working;uniform float u_any_working;uniform float u_intensity;
${code}
void main(){gl_FragColor=vec4(clamp(theme(gl_FragCoord.xy/u_resolution),0.,1.)*u_intensity,1.);}`

/** A passive, bounded renderer. No theme JS, texture inputs or access to app DOM.
 * GPU queries are asynchronous (never gl.finish); unsupported devices also use
 * submit time/frame lateness. This is load shedding, not a hard GPU time limit.
 * `onFps` receives the measured frame rate every few seconds while drawing. */
export function mountAppearanceShader(canvas: HTMLCanvasElement, config: AppearanceShader, status: (state: Status, ms?: number) => void, onFps?: (fps: number) => void) {
  const gl = canvas.getContext('webgl', { alpha: false, antialias: false, depth: false, stencil: false, powerPreference: 'low-power', preserveDrawingBuffer: false })
  if (!gl) {
    status('failed')
    return null
  }
  const shaders: WebGLShader[] = []
  let program: WebGLProgram | null = null
  let buffer: WebGLBuffer | null = null
  const disposeGL = () => {
    if (buffer) gl.deleteBuffer(buffer)
    if (program) gl.deleteProgram(program)
    for (const shader of shaders) gl.deleteShader(shader)
  }
  try {
    const compile = (type: number, source: string) => {
      const shader = gl.createShader(type)
      if (!shader) throw new Error('shader')
      shaders.push(shader)
      gl.shaderSource(shader, source)
      gl.compileShader(shader)
      if (!gl.getShaderParameter(shader, gl.COMPILE_STATUS)) throw new Error('compile')
      return shader
    }
    program = gl.createProgram()
    if (!program) throw new Error('program')
    gl.attachShader(program, compile(gl.VERTEX_SHADER, vertex))
    gl.attachShader(program, compile(gl.FRAGMENT_SHADER, shaderFragment(config.code)))
    gl.linkProgram(program)
    if (!gl.getProgramParameter(program, gl.LINK_STATUS)) throw new Error('link')
    gl.useProgram(program)
    buffer = gl.createBuffer()
    if (!buffer) throw new Error('buffer')
    gl.bindBuffer(gl.ARRAY_BUFFER, buffer)
    gl.bufferData(gl.ARRAY_BUFFER, new Float32Array([-1, -1, 3, -1, -1, 3]), gl.STATIC_DRAW)
    const position = gl.getAttribLocation(program, 'a_position')
    gl.enableVertexAttribArray(position)
    gl.vertexAttribPointer(position, 2, gl.FLOAT, false, 0, 0)
  } catch {
    disposeGL()
    status('failed')
    return null
  }
  const uniforms = Object.fromEntries(['u_time', 'u_resolution', 'u_accent', 'u_working', 'u_any_working'].map(name => [name, gl.getUniformLocation(program!, name)]))
  gl.uniform1f(gl.getUniformLocation(program, 'u_intensity'), config.intensity)
  const timer = gl.getExtension('EXT_disjoint_timer_query') as TimerExtension | null
  const meter = frameRateMeter(onFps)
  let query: object | null = null
  let timeout: ReturnType<typeof setTimeout> | undefined
  let raf: number | undefined
  let disposed = false, stopped = false, ready = false
  let fps = 30, overSince: number | undefined, due = 0, lastAt = 0, time = 0, gpuMS = 0
  let working = 0, anyWorking = 0
  let accent: [number, number, number] = [.3, .7, 1]
  const motion = matchMedia('(prefers-reduced-motion: reduce)')
  const cancel = () => {
    if (timeout !== undefined) clearTimeout(timeout)
    if (raf !== undefined) cancelAnimationFrame(raf)
    timeout = undefined
    raf = undefined
    lastAt = 0
    meter.reset()
  }
  const note = (ms: number, now: number) => {
    if (ms <= 33) {
      overSince = undefined
      return
    }
    overSince ??= now
    if (now - overSince < 2000) return
    overSince = undefined
    if (fps === 30) {
      fps = 15
      status('throttled', ms)
    } else {
      stopped = true
      cancel()
      status('stopped', ms)
    }
  }
  const draw = () => {
    if (disposed || stopped || document.hidden) return
    const start = performance.now()
    if (lastAt) time += Math.min(0.1, (start - lastAt) / 1000) * config.speed
    lastAt = start
    const scale = Math.min(1, 1280 / Math.max(1, innerWidth), 720 / Math.max(1, innerHeight))
    const width = Math.max(1, Math.floor(innerWidth * scale))
    const height = Math.max(1, Math.floor(innerHeight * scale))
    if (canvas.width !== width || canvas.height !== height) {
      canvas.width = width
      canvas.height = height
    }
    gl.viewport(0, 0, width, height)
    if (timer && query && timer.getQueryObjectEXT(query, timer.QUERY_RESULT_AVAILABLE_EXT)) {
      gpuMS = gl.getParameter(timer.GPU_DISJOINT_EXT) ? 0 : Number(timer.getQueryObjectEXT(query, timer.QUERY_RESULT_EXT)) / 1e6
      timer.deleteQueryEXT(query)
      query = null
    }
    if (stopped) return
    const measuring = timer && !query
    if (measuring) {
      query = timer.createQueryEXT()
      timer.beginQueryEXT(timer.TIME_ELAPSED_EXT, query)
    }
    gl.uniform1f(uniforms.u_time, time)
    gl.uniform2f(uniforms.u_resolution, width, height)
    gl.uniform3f(uniforms.u_accent, ...accent)
    gl.uniform1f(uniforms.u_working, working)
    gl.uniform1f(uniforms.u_any_working, anyWorking)
    gl.drawArrays(gl.TRIANGLES, 0, 3)
    if (measuring) timer.endQueryEXT(timer.TIME_ELAPSED_EXT)
    meter.frame(start)
    note(Math.max(gpuMS, performance.now() - start, due ? start - due : 0), start)
    if (!ready && !stopped) {
      ready = true
      status('ready')
    }
  }
  const loop = () => {
    timeout = undefined
    raf = undefined
    draw()
    if (disposed || stopped || document.hidden || motion.matches || config.speed === 0) return
    const wait = 1000 / fps
    due = performance.now() + wait
    timeout = setTimeout(() => {
      timeout = undefined
      raf = requestAnimationFrame(loop)
    }, wait)
  }
  const refresh = () => {
    cancel()
    due = 0
    overSince = undefined
    if (!document.hidden) loop()
  }
  const lost = (event: Event) => {
    event.preventDefault()
    stopped = true
    cancel()
    status('failed')
  }
  document.addEventListener('visibilitychange', refresh)
  motion.addEventListener('change', refresh)
  window.addEventListener('resize', refresh)
  canvas.addEventListener('webglcontextlost', lost)
  refresh()
  return {
    setUniforms: (nextAccent: [number, number, number], nextWorking: boolean, nextAnyWorking = nextWorking) => {
      accent = nextAccent
      working = Number(nextWorking)
      anyWorking = Number(nextAnyWorking)
      if (motion.matches || config.speed === 0) {
        lastAt = 0
        draw()
      }
    },
    destroy: () => {
      disposed = true
      cancel()
      document.removeEventListener('visibilitychange', refresh)
      motion.removeEventListener('change', refresh)
      window.removeEventListener('resize', refresh)
      canvas.removeEventListener('webglcontextlost', lost)
      if (timer && query) timer.deleteQueryEXT(query)
      disposeGL()
      gl.getExtension('WEBGL_lose_context')?.loseContext()
    },
  }
}
