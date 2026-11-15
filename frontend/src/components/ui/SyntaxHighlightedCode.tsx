import React, { useMemo } from 'react'
import { Prism as SyntaxHighlighter } from 'react-syntax-highlighter'
import { oneDark, prism } from 'react-syntax-highlighter/dist/esm/styles/prism'
import { useAppearanceStore } from '../../stores/useAppearanceStore'

interface SyntaxHighlightedCodeProps {
  code: string
  language: string
  isDark: boolean
  showLineNumbers?: boolean
  wrapLongLines?: boolean
}

const SyntaxHighlightedCode: React.FC<SyntaxHighlightedCodeProps> = ({ code, language, isDark, showLineNumbers = false, wrapLongLines = false }) => {
  const profile = useAppearanceStore(state => state.profile)
  const codeColors = useAppearanceStore(state => (state.preview ?? state.current).code?.[state.profile])
  const style = useMemo(() => {
    const base = (isDark ? oneDark : prism) as Record<string, React.CSSProperties>
    const pick = (name: string): string | undefined => {
      const value = codeColors?.[name as keyof typeof codeColors] as string | undefined
      return typeof value === 'string' && /^#[0-9a-f]{6}$/i.test(value) ? value : undefined
    }
    const overrides: Record<string, React.CSSProperties> = {}
    const keyword = pick('keyword')
    const str = pick('string')
    const num = pick('number')
    const comment = pick('comment')
    const fn = pick('function')
    const op = pick('operator')
    const fg = pick('foreground')
    if (keyword) overrides.keyword = { color: keyword }
    if (str) { overrides.string = { color: str }; overrides.char = { color: str }; overrides.attrValue = { color: str } }
    if (num) { overrides.number = { color: num }; overrides.boolean = { color: num }; overrides.constant = { color: num } }
    if (comment) { overrides.comment = { color: comment }; overrides.prolog = { color: comment }; overrides.doctype = { color: comment } }
    if (fn) { overrides.function = { color: fn }; overrides['class-name'] = { color: fn } }
    if (op) { overrides.operator = { color: op }; overrides.punctuation = { color: op }; overrides.attrName = { color: op } }
    if (fg) { overrides['code[class*="language-"]'] = { ...(base['code[class*="language-"]'] ?? {}), color: fg }; overrides['pre[class*="language-"]'] = { ...(base['pre[class*="language-"]'] ?? {}), color: fg } }
    if (Object.keys(overrides).length === 0) return base as { [key: string]: React.CSSProperties }
    return { ...base, ...overrides } as { [key: string]: React.CSSProperties }
  }, [isDark, codeColors])
  const background = codeColors?.background && /^#[0-9a-f]{6}$/i.test(codeColors.background) ? codeColors.background : isDark ? '#1f2937' : '#f9fafb'
  const foreground = codeColors?.foreground && /^#[0-9a-f]{6}$/i.test(codeColors.foreground) ? codeColors.foreground : undefined
  void profile
  return (
    <SyntaxHighlighter
      style={style}
      language={language}
      PreTag="div"
      showLineNumbers={showLineNumbers}
      wrapLongLines={wrapLongLines}
      className="!m-0 !p-0"
      customStyle={{
        margin: 0,
        padding: '1rem',
        borderRadius: '0.5rem',
        fontSize: 'var(--app-code-size, 0.875rem)',
        fontFamily: 'var(--app-code-font, monospace)',
        lineHeight: '1.6',
        overflowX: 'auto',
        maxWidth: '100%',
        width: '100%',
        boxSizing: 'border-box',
        background,
        ...(foreground ? { color: foreground } : {}),
      }}
      codeTagProps={{
        style: {
          display: 'block',
          overflowX: 'auto',
          maxWidth: '100%',
          fontFamily: 'inherit',
          background: 'transparent',
        },
      }}
    >
      {code}
    </SyntaxHighlighter>
  )
}

export default SyntaxHighlightedCode
