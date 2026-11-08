import React from 'react'
import { Prism as SyntaxHighlighter } from 'react-syntax-highlighter'
import { oneDark, prism } from 'react-syntax-highlighter/dist/esm/styles/prism'

interface SyntaxHighlightedCodeProps {
  code: string
  language: string
  isDark: boolean
  showLineNumbers?: boolean
}

const SyntaxHighlightedCode: React.FC<SyntaxHighlightedCodeProps> = ({ code, language, isDark, showLineNumbers = false }) => (
  <SyntaxHighlighter
    style={isDark ? (oneDark as { [key: string]: React.CSSProperties }) : (prism as { [key: string]: React.CSSProperties })}
    language={language}
    PreTag="div"
    showLineNumbers={showLineNumbers}
    className="!m-0 !p-0"
    customStyle={{
      margin: 0,
      padding: '1rem',
      borderRadius: '0.5rem',
      fontSize: '0.875rem',
      lineHeight: '1.6',
      overflowX: 'auto',
      maxWidth: '100%',
      width: '100%',
      boxSizing: 'border-box',
      background: isDark ? '#1f2937' : '#f9fafb',
    }}
    codeTagProps={{
      style: {
        display: 'block',
        overflowX: 'auto',
        maxWidth: '100%',
      },
    }}
  >
    {code}
  </SyntaxHighlighter>
)

export default SyntaxHighlightedCode
