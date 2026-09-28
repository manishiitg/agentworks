import js from '@eslint/js'
import globals from 'globals'
import reactHooks from 'eslint-plugin-react-hooks'
import reactRefresh from 'eslint-plugin-react-refresh'
import tseslint from 'typescript-eslint'
import { globalIgnores } from 'eslint/config'

export default tseslint.config([
  globalIgnores(['dist', 'src/generated']),
  {
    files: ['**/*.{ts,tsx}'],
    extends: [
      js.configs.recommended,
      tseslint.configs.recommended,
      reactHooks.configs['recommended-latest'],
      reactRefresh.configs.vite,
    ],
    languageOptions: {
      ecmaVersion: 2020,
      globals: globals.browser,
    },
    rules: {
      '@typescript-eslint/no-unused-vars': [
        'error',
        {
          argsIgnorePattern: '^_',
          caughtErrorsIgnorePattern: '^_',
          varsIgnorePattern: '^_',
        },
      ],
    },
  },
  {
    // Tests intentionally construct malformed and partial wire events.
    files: ['**/*.{test,spec}.{ts,tsx}'],
    rules: {
      '@typescript-eslint/no-explicit-any': 'off',
    },
  },
  {
    // These modules intentionally co-locate component-bound render helpers,
    // contexts, or hooks that are consumed as part of the component API.
    files: [
      'src/components/CleanupOldChatsDropdown.tsx',
      'src/components/PreviousChatHistoryPanel.tsx',
      'src/components/ui/ConversationRenderer.tsx',
      'src/components/ui/MarkdownRenderer.tsx',
      'src/components/ui/RenderedContentSearch.tsx',
      'src/components/workflow/ReportViewer.tsx',
      'src/components/workflow/reportWidgets/reportEmbedContext.tsx',
      'src/components/workflow/reportWidgets/shared.tsx',
      'src/products/work/WorkSurface.tsx',
    ],
    rules: {
      'react-refresh/only-export-components': 'off',
    },
  },
])
