[← app / integrations](index.md)

# PLAT-620: Name Connected and Available tabs explicitly as MCPs

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | app |
| Area | integrations |
| Summary | The Tools & secrets tabs said only Connected and Available, obscuring that both browse MCP integrations. |

## What happened

User screenshot of Integrations → Tools & secrets: `Connected`, `Available`,
`Secrets`, `Skills`, `Vault`. The first two labels do not identify their content,
so the user asked to show MCP explicitly.

## Fix

Rename the shared project tabs to `Connected MCPs` and `Available MCPs`, covering
Workflow, Crew, Code and Relay. Use the same labels in the standalone shared MCP
browser. Tab IDs, saved selection and connection actions stay compatible.
Update existing tab navigation checks to match the visible wording.

Validation: 35 existing checks passed across six integration, workspace and
workflow suites. The full frontend production build, TypeScript, release asset
validation and bundle budget passed. Targeted ESLint passes with the existing
Fast Refresh export-pattern rule excluded; its two diagnostics in
`ProjectPluginsPanel.tsx` are identical on unchanged origin/main.

## Left

Deploy to Confida and verify the current public assets contain both labels.
