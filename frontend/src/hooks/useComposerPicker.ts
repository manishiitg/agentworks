import { useEffect, type RefObject } from 'react'

export interface ComposerPickerProps {
  inputRef?: RefObject<HTMLTextAreaElement | HTMLInputElement | null>
  listId?: string
  onActiveOptionChange?: (id: string | undefined) => void
}

/** Dismiss when leaving the input, popup, and optional toggle button. */
export function useComposerPicker(
  isOpen: boolean,
  dialogRef: RefObject<HTMLDivElement | null>,
  inputRef: ComposerPickerProps['inputRef'],
  onClose: (reason?: 'outside' | 'resize') => void,
  triggerRef?: RefObject<HTMLElement | null>,
) {
  useEffect(() => {
    if (!isOpen) return
    const dismissOutside = (event: Event) => {
      const target = event.target as Node | null
      if (target && !dialogRef.current?.contains(target) && target !== inputRef?.current && !triggerRef?.current?.contains(target)) onClose('outside')
    }
    const dismissOnResize = () => onClose('resize')
    window.addEventListener('resize', dismissOnResize)
    document.addEventListener('mousedown', dismissOutside)
    document.addEventListener('focusin', dismissOutside)
    return () => {
      window.removeEventListener('resize', dismissOnResize)
      document.removeEventListener('mousedown', dismissOutside)
      document.removeEventListener('focusin', dismissOutside)
    }
  }, [isOpen, dialogRef, inputRef, onClose, triggerRef])
}

export function isComposerPickerEvent(event: KeyboardEvent, inputRef: ComposerPickerProps['inputRef']) {
  // Legacy standalone callers have no input. Integrated pickers must never catch another input's keys.
  return !event.defaultPrevented && (!inputRef || event.target === inputRef.current)
}
