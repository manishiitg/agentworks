import { readFileSync } from 'fs'
import { describe, expect, it } from 'vitest'

// Owner rule (one company per server): people see two modes only, Builder and
// Run. The words read-only, viewer, reader and guest must not come back as
// labels in these access and sharing components. API values and code
// identifiers (reader, read_only, vault_reader) are unchanged and out of scope.
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

describe('Builder/Run wording', () => {
  it('keeps forbidden mode words out of visible labels', () => {
    for (const file of FILES) {
      const source = readFileSync(new URL(`../${file}`, import.meta.url), 'utf8')
      // Only rendered text, labels and titles: strip comments first.
      const visible = source.replace(/\/\*[\s\S]*?\*\//g, '').replace(/^\s*\/\/.*$/gm, '')
      for (const pattern of FORBIDDEN) {
        expect(visible, `${file} matches ${pattern}`).not.toMatch(pattern)
      }
    }
  })
})
