import React from 'react'

// Text nodes only: snippets and query are untrusted, never HTML.
export function SearchMatch({ text, query }: { text: string; query: string }) {
  const needle = query.trim().toLocaleLowerCase()
  if (!needle) return <>{text}</>
  const parts: React.ReactNode[] = []
  let from = 0
  const lower = text.toLocaleLowerCase()
  for (let at = lower.indexOf(needle); at >= 0; at = lower.indexOf(needle, from)) {
    parts.push(text.slice(from, at), <mark key={at} className="rounded-sm bg-yellow-200 text-yellow-900 dark:bg-yellow-900 dark:text-yellow-100">{text.slice(at, at + needle.length)}</mark>)
    from = at + needle.length
  }
  parts.push(text.slice(from))
  return <>{parts}</>
}
