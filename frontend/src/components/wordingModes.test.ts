import { readFileSync } from 'fs'
import { describe, expect, it } from 'vitest'

// Owner rule (one company per server): people see two modes only, Builder and
// Run. The words read-only, viewer, reader and guest must not come back in
// rendered text or label/title attributes of these access and sharing
// components. API values and code identifiers (value="reader", read_only,
// vault_reader) are unchanged and out of scope, so only JSX text and
// title/aria-label/placeholder/description strings are scanned.
const FILES = [
  'components/workflow/WorkflowSharePopup.tsx',
  'components/workflow/WorkflowFolderAccessView.tsx',
  'components/workflow/WorkflowTeamView.tsx',
  'components/folders/WorkflowReferenceAccess.tsx',
  'products/knowledgebase/KnowledgebaseAccessPanel.tsx',
  'products/work/SharedCrewFilesPanel.tsx',
  'components/ToolSelectionSection.tsx',
  'hooks/useCanWriteWorkflow.ts',
  'components/MCPConfigEditor.tsx',
  'components/ModePresetBar.tsx',
  'components/TerminalCenter.tsx',
  'components/scheduler/scheduleRuns/AfterRunActions.tsx',
  'components/integrations/LocalMcpTokenPanel.tsx',
  'pages/CLIOAuthConsent.tsx',
  'pages/MCPOAuthConsent.tsx',
]
const FORBIDDEN = [/read-only/i, /READ-ONLY/, /\bviewer\b/i, /\breader\b/i, /\bguest\b/i, /Read only/]

function visibleStrings(source: string): string[] {
  const withoutComments = source.replace(/\/\*[\s\S]*?\*\//g, '').replace(/^\s*\/\/.*$/gm, '')
  const texts: string[] = []
  for (const match of withoutComments.matchAll(/>([^<>{}]+)</g)) texts.push(match[1])
  for (const match of withoutComments.matchAll(/(?:title|aria-label|placeholder|description|label)\s*=\s*(?:"([^"]+)"|\{`([^`]+)`\}|\{'([^']+)'\})/g)) {
    texts.push(match[1] || match[2] || match[3] || '')
  }
  return texts
}

describe('Builder/Run wording', () => {
  it('keeps forbidden mode words out of visible labels', () => {
    for (const file of FILES) {
      const source = readFileSync(new URL(`../${file}`, import.meta.url), 'utf8')
      for (const text of visibleStrings(source)) {
        for (const pattern of FORBIDDEN) {
          expect(text, `${file}: "${text.trim().slice(0, 60)}" matches ${pattern}`).not.toMatch(pattern)
        }
      }
    }
  })
})
