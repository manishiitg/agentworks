import type { DragEvent } from 'react'

/** A project file dragged from Files or Changes onto the message box (owner, 20 Nov).
 * It joins the message as the same @file reference the @ picker adds. */
export const CHAT_FILE_MIME = 'application/x-agentworks-file'
export type ChatFileDrag = { path: string; name: string; type: 'file' | 'folder' }

export function chatFileDragProps(path: string, type: 'file' | 'folder' = 'file') {
  return {
    draggable: true,
    onDragStart: (event: DragEvent) => {
      const item: ChatFileDrag = { path, name: path.split('/').filter(Boolean).pop() || path, type }
      event.dataTransfer.setData(CHAT_FILE_MIME, JSON.stringify(item))
      event.dataTransfer.setData('text/plain', path)
      event.dataTransfer.effectAllowed = 'copy'
    },
  }
}

export const hasChatFileDrag = (event: DragEvent) => !!event.dataTransfer?.types?.includes(CHAT_FILE_MIME)

export function readChatFileDrag(event: DragEvent): ChatFileDrag | null {
  try {
    const item = JSON.parse(event.dataTransfer.getData(CHAT_FILE_MIME)) as ChatFileDrag
    return item && typeof item.path === 'string' && item.path ? item : null
  } catch { return null }
}
