# Pi native MCP and AGY certification — 2026-09-30

Pi 0.99.1 uses its native `builtin:mcp` extension with a private session
`mcp.json`. The SDK no longer loads `npm:pi-mcp-adapter`, and the platform
updater no longer manages that package. Direct MCP tool exposure, private
configuration, literal tool descriptions, cleanup and continuation are covered
by the Pi adapter tests.

The live Pi P0 selection passed 15 SDK tests with no failures or skips, plus
retained chat, workflow notification and automatic workflow advancement checks.
The shared turn, input, completion, progress, provider account and frontend
checks passed. Native MCP configuration isolation and actual streamed answer
formatting were also checked.

AGY 1.2.14 passed its 19 registered SDK P0 tests with no failures or skips,
plus the same three application checks. The official updater reported 1.2.14
as current. Tests used the existing Pi Google API key through
`GEMINI_API_KEY`/`GOOGLE_API_KEY` in isolated Gemini API key mode; no OAuth
credentials were copied.

AGY fixes prevent copying an active SQLite conversation index into a private
home and ensure completion and progress polling initialize the same receipt
deduplication state. Formatting validation compares the returned answer with
the same turn's independently saved native answer. The workflow P0 fixture
uses agentic `message_sequence` steps rather than saved-script `regular` steps.

A separate clean SDK `main` run on AGY 1.2.14 passed
`TestAgyCLIRealHybridNativeReadAndWriteDenial`,
`TestAgyToolModeHookDecisions` and
`TestAgyToolModeHookFailsClosedOnParseOrInterpreterError`. Native file reading
worked, and native writes remain denied in hybrid mode.

## Scope and remaining gap

The application P0 runs used isolated local services and the local development
sources at certification time. They did not certify the later platform rollout
on Linux. Other providers and the optional two-account live matrix were not
selected. The existing user app was not rebuilt or restarted.

**Historical scope of this initial Pi/AGY certification:** AGY Full CLI was not implemented by this initial change. The platform
can upgrade its tool mode to `full` under Landlock, but mcpagent still maps AGY
to `hybrid`; its hook denies native shell commands, file writes and subagents.
The later [AGY local Full CLI integration](../design/agy_full_native_tools.md)
adds explicit mode handling and local native-tool checks. Linux containment
checks remain outside that local certification. Native MCP support alone does
not enable those tools.

Before publication, the Pi and AGY adapter unit suites, formatting contracts,
bridge routing checks, platform plugin updater, retained-mode wiring, version
floors and workflow provider matrix were checked against current remote
`main` with these changes applied. The newer remote certification entries for
other providers were preserved.
