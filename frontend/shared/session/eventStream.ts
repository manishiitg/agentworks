// Shared SSE framing for chat and live notices; authentication stays in headers.
export interface EventStreamFrame {
  event: string
  data: string
  id?: string
}

export async function readEventStream(
  body: ReadableStream<Uint8Array>,
  signal: AbortSignal,
  onFrame: (frame: EventStreamFrame) => void,
): Promise<void> {
  const reader = body.getReader()
  const decoder = new TextDecoder()
  let buffer = ''
  let event = ''
  let id: string | undefined
  let dataLines: string[] = []
  const flush = () => {
    if (dataLines.length > 0 && !signal.aborted) onFrame({ event, data: dataLines.join('\n'), id })
    event = ''
    id = undefined
    dataLines = []
  }
  const cancel = () => { void reader.cancel().catch(() => {}) }
  signal.addEventListener('abort', cancel, { once: true })
  try {
    while (!signal.aborted) {
      const { value, done } = await reader.read()
      if (done || signal.aborted) break
      buffer += decoder.decode(value, { stream: true })
      let nl: number
      while ((nl = buffer.indexOf('\n')) >= 0) {
        const line = buffer.slice(0, nl).replace(/\r$/, '')
        buffer = buffer.slice(nl + 1)
        if (line === '') { flush(); continue }
        if (line.startsWith(':')) continue
        if (line.startsWith('id:')) { id = line.slice(3).trim(); continue }
        if (line.startsWith('event:')) { event = line.slice(6).trim(); continue }
        if (line.startsWith('data:')) dataLines.push(line.slice(5).replace(/^ /, ''))
      }
    }
    // Only a blank line dispatches a complete frame; an interrupted frame is
    // replayed from the last complete cursor after reconnecting.
  } finally {
    signal.removeEventListener('abort', cancel)
    reader.releaseLock()
  }
}
