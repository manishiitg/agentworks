import React, { useState } from 'react'
import { Check, Copy } from 'lucide-react'
import { copyToClipboard } from '../../utils/textUtils'

// Copy control for a fenced code block in a chat reply (MarkdownRenderer copyableCode).
export const CodeCopyButton: React.FC<{ text: string }> = ({ text }) => {
  const [copied, setCopied] = useState(false)
  const copy = async () => {
    if (!(await copyToClipboard(text))) return
    setCopied(true)
    window.setTimeout(() => setCopied(false), 1500)
  }
  return (
    <button
      type="button"
      onClick={() => void copy()}
      title={copied ? 'Copied' : 'Copy'}
      aria-label={copied ? 'Copied' : 'Copy code'}
      className="absolute right-2 top-2 z-10 inline-flex h-7 items-center gap-1 rounded-md border border-gray-200 bg-white/90 px-2 text-xs text-gray-600 shadow-sm hover:bg-gray-50 dark:border-gray-600 dark:bg-gray-800/90 dark:text-gray-300 dark:hover:bg-gray-700"
    >
      {copied ? <Check className="h-3.5 w-3.5" /> : <Copy className="h-3.5 w-3.5" />}
      {copied ? 'Copied' : 'Copy'}
    </button>
  )
}

// The plain text of a code element's children (react-markdown gives strings or nested elements).
export function codeBlockText(node: React.ReactNode): string {
  if (typeof node === 'string' || typeof node === 'number') return String(node)
  if (Array.isArray(node)) return node.map(codeBlockText).join('')
  if (React.isValidElement<{ children?: React.ReactNode }>(node)) return codeBlockText(node.props.children)
  return ''
}
