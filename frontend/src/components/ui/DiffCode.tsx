import type React from 'react'
import { Prism as SyntaxHighlighter } from 'react-syntax-highlighter'
import { oneDark, oneLight } from 'react-syntax-highlighter/dist/esm/styles/prism'

// One diff line's code, highlighted inline with no background of its own, so the
// row's added/removed tint shows through. Loaded lazily with the highlighter.
export default function DiffCode({ text, language, isDark }: { text: string; language: string; isDark: boolean }) {
  return <SyntaxHighlighter
    language={language}
    style={(isDark ? oneDark : oneLight) as { [key: string]: React.CSSProperties }}
    PreTag="span"
    CodeTag="span"
    customStyle={{ background: 'transparent', padding: 0, margin: 0, display: 'inline', fontSize: 'inherit', lineHeight: 'inherit' }}
    codeTagProps={{ style: { background: 'transparent', fontSize: 'inherit', lineHeight: 'inherit', fontFamily: 'inherit' } }}
  >{text}</SyntaxHighlighter>
}
