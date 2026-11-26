import { useCallback, useEffect, useRef, useState, type RefObject } from 'react'
import { ChevronDown, ChevronUp, Search, X } from 'lucide-react'

const baseMatchClass = 'rounded-sm bg-warning/35 px-0.5 text-foreground'
const currentMatchClass = 'ring-2 ring-primary bg-primary/25'
const SEARCH_DEBOUNCE_MS = 160
const searchMarkStyle = 'background-color: hsl(var(--warning) / 0.35); color: hsl(var(--foreground));'
const currentSearchMarkStyle = `${searchMarkStyle} box-shadow: 0 0 0 2px hsl(var(--primary)); background-color: hsl(var(--primary) / 0.25);`

const useDebouncedValue = <T,>(value: T, delayMs: number): T => {
  const [debouncedValue, setDebouncedValue] = useState(value)

  useEffect(() => {
    const timeout = window.setTimeout(() => setDebouncedValue(value), delayMs)
    return () => window.clearTimeout(timeout)
  }, [delayMs, value])

  return debouncedValue
}

const clearSearchMarks = (root: HTMLElement) => {
  const marks = Array.from(root.querySelectorAll<HTMLElement>('[data-rendered-content-search-mark="true"]'))
  const parents = new Set<Node>()

  for (const mark of marks) {
    const parent = mark.parentNode
    if (!parent) continue
    parents.add(parent)
    parent.replaceChild(document.createTextNode(mark.textContent || ''), mark)
  }

  parents.forEach(parent => parent.normalize())
}

const markSearchMatches = (root: HTMLElement, query: string): HTMLElement[] => {
  const normalizedQuery = query.toLowerCase()
  const textNodes: Text[] = []
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT, {
    acceptNode(node) {
      const value = node.nodeValue
      const parent = node.parentElement

      if (!value || !parent || !value.toLowerCase().includes(normalizedQuery)) {
        return NodeFilter.FILTER_REJECT
      }

      if (
        parent.closest('script, style, textarea, input, [data-rendered-content-search-mark="true"]') ||
        parent.isContentEditable
      ) {
        return NodeFilter.FILTER_REJECT
      }

      return NodeFilter.FILTER_ACCEPT
    },
  })

  let current = walker.nextNode()
  while (current) {
    textNodes.push(current as Text)
    current = walker.nextNode()
  }

  const marks: HTMLElement[] = []

  for (const node of textNodes) {
    const text = node.nodeValue || ''
    const lowerText = text.toLowerCase()
    const fragment = document.createDocumentFragment()
    let lastIndex = 0
    let matchIndex = lowerText.indexOf(normalizedQuery)

    while (matchIndex !== -1) {
      if (matchIndex > lastIndex) {
        fragment.appendChild(document.createTextNode(text.slice(lastIndex, matchIndex)))
      }

      const mark = document.createElement('span')
      mark.dataset.renderedContentSearchMark = 'true'
      mark.className = baseMatchClass
      mark.setAttribute('style', searchMarkStyle)
      mark.textContent = text.slice(matchIndex, matchIndex + query.length)
      marks.push(mark)
      fragment.appendChild(mark)

      lastIndex = matchIndex + query.length
      matchIndex = lowerText.indexOf(normalizedQuery, lastIndex)
    }

    if (lastIndex < text.length) {
      fragment.appendChild(document.createTextNode(text.slice(lastIndex)))
    }

    node.parentNode?.replaceChild(fragment, node)
  }

  return marks
}

export interface RenderedContentSearchState {
  isOpen: boolean
  query: string
  matchCount: number
  currentIndex: number
  inputRef: RefObject<HTMLInputElement | null>
  open: () => void
  close: () => void
  setQuery: (query: string) => void
  next: () => void
  previous: () => void
}

interface UseRenderedContentSearchOptions {
  targetRef: RefObject<HTMLElement | null>
  contentKey?: string
  enabled?: boolean
  useKeyboardShortcut?: boolean
}

export const useRenderedContentSearch = ({
  targetRef,
  contentKey,
  enabled = true,
  useKeyboardShortcut = true,
}: UseRenderedContentSearchOptions): RenderedContentSearchState => {
  const inputRef = useRef<HTMLInputElement>(null)
  const marksRef = useRef<HTMLElement[]>([])
  const [isOpen, setIsOpen] = useState(false)
  const [query, setQueryState] = useState('')
  const [matchCount, setMatchCount] = useState(0)
  const [currentIndex, setCurrentIndex] = useState(0)
  const debouncedQuery = useDebouncedValue(query, SEARCH_DEBOUNCE_MS)

  const open = useCallback(() => {
    if (!enabled) return
    setIsOpen(true)
    window.requestAnimationFrame(() => {
      inputRef.current?.focus()
      inputRef.current?.select()
    })
  }, [enabled])

  const close = useCallback(() => {
    setIsOpen(false)
    setQueryState('')
    setMatchCount(0)
    setCurrentIndex(0)
  }, [])

  const setQuery = useCallback((nextQuery: string) => {
    setCurrentIndex(0)
    setQueryState(nextQuery)
  }, [])

  const next = useCallback(() => {
    setCurrentIndex(index => matchCount > 0 ? (index + 1) % matchCount : 0)
  }, [matchCount])

  const previous = useCallback(() => {
    setCurrentIndex(index => matchCount > 0 ? (index - 1 + matchCount) % matchCount : 0)
  }, [matchCount])

  useEffect(() => {
    if (!enabled) {
      setIsOpen(false)
      setQueryState('')
      setMatchCount(0)
      setCurrentIndex(0)
    }
  }, [enabled])

  useEffect(() => {
    const root = targetRef.current
    if (root) {
      clearSearchMarks(root)
    }
    marksRef.current = []

    const trimmedQuery = debouncedQuery.trim()
    if (!enabled || !isOpen || !root || !trimmedQuery) {
      setMatchCount(0)
      setCurrentIndex(0)
      return
    }

    const marks = markSearchMatches(root, trimmedQuery)
    marksRef.current = marks
    setMatchCount(marks.length)
    setCurrentIndex(index => marks.length > 0 ? Math.min(index, marks.length - 1) : 0)

    return () => {
      clearSearchMarks(root)
      marksRef.current = []
    }
  }, [contentKey, debouncedQuery, enabled, isOpen, targetRef])

  useEffect(() => {
    const root = targetRef.current
    const trimmedQuery = debouncedQuery.trim()
    if (enabled && isOpen && root && trimmedQuery && matchCount > 0) {
      const marksWereRemoved = marksRef.current.length === 0 || marksRef.current.some(mark => !mark.isConnected)
      if (marksWereRemoved) {
        clearSearchMarks(root)
        marksRef.current = markSearchMatches(root, trimmedQuery)
      }
    }

    marksRef.current.forEach((mark, index) => {
      mark.className = `${baseMatchClass} ${index === currentIndex ? currentMatchClass : ''}`
      mark.setAttribute('style', index === currentIndex ? currentSearchMarkStyle : searchMarkStyle)
    })

    const currentMark = marksRef.current[currentIndex]
    if (currentMark) {
      currentMark.scrollIntoView({ behavior: 'smooth', block: 'center', inline: 'nearest' })
    }
  }, [currentIndex, debouncedQuery, enabled, isOpen, matchCount, targetRef])

  useEffect(() => {
    if (!enabled || !useKeyboardShortcut) return

    const handleKeyDown = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'f') {
        event.preventDefault()
        open()
      } else if (isOpen && (event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'g') {
        event.preventDefault()
        if (event.shiftKey) {
          previous()
        } else {
          next()
        }
      } else if (event.key === 'Escape' && isOpen) {
        event.preventDefault()
        close()
      }
    }

    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [close, enabled, isOpen, next, open, previous, useKeyboardShortcut])

  return {
    isOpen,
    query,
    matchCount,
    currentIndex,
    inputRef,
    open,
    close,
    setQuery,
    next,
    previous,
  }
}

interface RenderedContentSearchButtonProps {
  search: RenderedContentSearchState
  className?: string
}

export const RenderedContentSearchButton = ({ search, className }: RenderedContentSearchButtonProps) => (
  <button
    type="button"
    onClick={search.open}
    className={className || 'flex items-center p-1.5 text-muted-foreground transition-colors hover:bg-muted hover:text-foreground    rounded-md'}
    title="Find in file (⌘F)"
  >
    <Search className="h-4 w-4" />
  </button>
)

interface RenderedContentSearchBarProps {
  search: RenderedContentSearchState
}

export const RenderedContentSearchBar = ({ search }: RenderedContentSearchBarProps) => {
  if (!search.isOpen) return null

  const trimmedQuery = search.query.trim()
  const matchLabel = !trimmedQuery
    ? ''
    : search.matchCount === 0
      ? 'No Results'
      : `${search.currentIndex + 1} of ${search.matchCount}`

  return (
    <div className="fixed left-1/2 top-16 z-50 w-[calc(100vw-1.5rem)] max-w-[460px] -translate-x-1/2 rounded-lg border border-border bg-popover/95 p-2 text-popover-foreground shadow-2xl shadow-black/15 backdrop-blur">
      <div className="flex items-center gap-1.5">
        <div className="relative min-w-0 flex-1">
          <Search className="pointer-events-none absolute left-3 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
          <input
            ref={search.inputRef}
            type="search"
            value={search.query}
            onChange={event => search.setQuery(event.target.value)}
            onKeyDown={event => {
              if (event.key === 'Enter') {
                event.preventDefault()
                if (event.shiftKey) {
                  search.previous()
                } else {
                  search.next()
                }
              } else if (event.key === 'ArrowDown') {
                event.preventDefault()
                search.next()
              } else if (event.key === 'ArrowUp') {
                event.preventDefault()
                search.previous()
              } else if (event.key === 'Escape') {
                event.preventDefault()
                search.close()
              }
            }}
            className="h-8 w-full rounded-full border border-input bg-background pl-8 pr-24 text-sm text-foreground outline-none transition-colors placeholder:text-muted-foreground focus:border-ring focus:ring-2 focus:ring-ring/25"
            placeholder="Find"
          />
          <span className="pointer-events-none absolute right-3 top-1/2 max-w-20 -translate-y-1/2 truncate text-right text-[11px] tabular-nums text-muted-foreground">
            {matchLabel}
          </span>
        </div>
        <button
          type="button"
          onClick={search.previous}
          disabled={search.matchCount === 0}
          className="rounded-full p-1.5 text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground disabled:cursor-not-allowed disabled:opacity-40"
          title="Previous match"
        >
          <ChevronUp className="h-4 w-4" />
        </button>
        <button
          type="button"
          onClick={search.next}
          disabled={search.matchCount === 0}
          className="rounded-full p-1.5 text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground disabled:cursor-not-allowed disabled:opacity-40"
          title="Next match"
        >
          <ChevronDown className="h-4 w-4" />
        </button>
        <button
          type="button"
          onClick={search.close}
          className="rounded-full p-1.5 text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground"
          title="Close search"
        >
          <X className="h-4 w-4" />
        </button>
      </div>
    </div>
  )
}
