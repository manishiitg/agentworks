import { useState } from 'react'
import { productRoutePath } from '../utils/productRoutes'
import { readProductNavigation } from '../utils/productUrlNavigation'

export function CopyProductLink() {
  const [result, setResult] = useState('')
  return <span className="inline-flex items-center gap-2"><button type="button" className="rounded-md border border-border px-3 py-1.5 text-xs hover:bg-muted" onClick={() => {
    if (!navigator.clipboard) { setResult('Could not copy link'); return }
    const link = window.location.origin + productRoutePath(readProductNavigation())
    void navigator.clipboard.writeText(link).then(() => setResult('Link copied'), () => setResult('Could not copy link'))
  }}>Copy link</button><span role="status" className="text-xs text-muted-foreground">{result}</span></span>
}
