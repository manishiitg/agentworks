import React, { useContext } from 'react'
import { Prism as SyntaxHighlighter } from 'react-syntax-highlighter'
import { oneDark, prism } from 'react-syntax-highlighter/dist/esm/styles/prism'
import { ThemeContext } from '../../contexts/ThemeContext'

interface SyntaxHighlightedCodeProps {
  code: string
  language: string
  isDark: boolean
  showLineNumbers?: boolean
  wrapLongLines?: boolean
}

function cleanPrismTheme(base: Record<string, React.CSSProperties>): Record<string, React.CSSProperties> {
  const next: Record<string, React.CSSProperties> = { ...base }
  for (const key of ['code[class*="language-"]', 'pre[class*="language-"]', 'operator']) {
    if (next[key]) {
      const { background: _bg, backgroundColor: _bgColor, ...rest } = next[key]
      next[key] = { ...rest, background: 'transparent' }
    }
  }
  return next
}

const DARK_STYLE = cleanPrismTheme(oneDark as Record<string, React.CSSProperties>)
const LIGHT_STYLE = cleanPrismTheme(prism as Record<string, React.CSSProperties>)

const SyntaxHighlightedCode: React.FC<SyntaxHighlightedCodeProps> = ({ code, language, isDark, showLineNumbers = false, wrapLongLines = false }) => {
  const themeCtx = useContext(ThemeContext)
  const effectiveDark = themeCtx ? themeCtx.theme === 'dark' : isDark
  return (
    <SyntaxHighlighter
      data-theme-surface="code"
      style={effectiveDark ? DARK_STYLE : LIGHT_STYLE}
      language={language}
      PreTag="div"
      showLineNumbers={showLineNumbers}
      wrapLongLines={wrapLongLines}
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
        background: effectiveDark ? '#1f2937' : '#f9fafb',
      }}
      codeTagProps={{
        style: {
          display: 'block',
          overflowX: 'auto',
          maxWidth: '100%',
          background: 'transparent',
        },
      }}
    >
      {code}
    </SyntaxHighlighter>
  )
}

export default SyntaxHighlightedCode
