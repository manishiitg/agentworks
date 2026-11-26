package claudecode

// The pane captured when TestClaudeCodeTmuxIntegrationHaikuLiveInputAndEscape
// timed out: a short multi-line live message sat inline in the composer (no
// "[Pasted text" chip) while the turn's spinner kept the pane changing.
const claudeInlineMultilineDraftPane = `❯ Call the api-bridge slow_contract MCP tool with token SLOW_CLAUDE_E2E_a78e3fd9 and delay_ms 30000. Do not answer
  until the tool returns. Then reply exactly with the tool result text.

⏺ Calling api-bridge… (ctrl+o to expand)

✽ Ionizing… (7s · ↓ 138 tokens)

──────────────────────────────────────────────────────────── mcp-agent-20260923-130442 ─
❯ ## Pre-validation failed (retry attempt 3)

  Fix the specific issue above and re-produce the required outputs.
────────────────────────────────────────────────────────────────────────────────────────
  ⏵⏵ don't ask on (shift+tab to cycle)`
