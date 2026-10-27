import { useEffect, useState } from 'react'

export type PreviewContent = { isText: boolean; content: string }

/** Recheck edited files without remounting an unchanged interactive page. */
export function useFilePreview(path: string | null, refreshKey: number, read: (path: string) => Promise<PreviewContent>): PreviewContent | null {
  const [loaded, setLoaded] = useState<{ path: string; file: PreviewContent } | null>(null)
  useEffect(() => {
    if (!path) { setLoaded(null); return }
    let cancelled = false
    read(path).then((file) => {
      if (cancelled) return
      setLoaded((previous) => previous?.path === path && previous.file.isText === file.isText && previous.file.content === file.content
        ? previous
        : { path, file })
    }).catch(() => {
      if (!cancelled) setLoaded({ path, file: { isText: false, content: '' } })
    })
    return () => { cancelled = true }
  }, [path, refreshKey, read])
  // A different file must never show the preceding file's contents while loading.
  return loaded?.path === path ? loaded.file : null
}
