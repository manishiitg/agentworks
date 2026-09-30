# Codex CLI 0.159.2 certification — 2026-09-30

Status: certified.

Scope: Codex only, macOS arm64, tmux transport, default-account stored login, Go 1.26.0. SDK and workflow advancement use GPT-6.1 Sol; the two HTTP application checks use GPT-6 Luna. Other CLI version claims are unchanged.

## SDK checks

All 19 registered live P0 cases passed without skips. Deterministic account, turn and progress preflights also passed.

| Contract | Seconds |
| --- | ---: |
| `TestCodexCLIRealDurableAckContract` | 27.16 |
| `TestCodexCLIRealInteractiveLiveInputAndEscapeContract` | 11.7 |
| `TestCodexCLIRealInteractiveLiveInputSteersBusyTurnContract` | 25.5 |
| `TestCodexCLIRealInteractiveMCPBridgeContract` | 21.79 |
| `TestCodexCLIRealInteractiveParallelIsolation` | 12.14 |
| `TestCodexCLIRealInteractiveQueuedValidationDoesNotCompleteDuringMCPTool` | 20.46 |
| `TestCodexCLIRealInteractiveStalledTurnDiagnosisContract` | 10.75 |
| `TestCodexCLIRealInteractiveTmuxFullContract` | 16.87 |
| `TestCodexCLIRealInteractiveWorkingDirectoryContract` | 22.84 |
| `TestCodexCLIRealInteractiveWorkspaceTrustPromptContract` | 9.25 |
| `TestCodexCLIRealMCPBridgeFileFinalExtractionContract` | 21.7 |
| `TestCodexCLIRealReplyFormattingFidelityE2E` | 14.63 |
| `TestCodexCLIRealRuntimeSelfCheckContract` | 26.06 |
| `TestCodexCLIStructuredTwoTurnResume` | 15.9 |
| `TestCodexCLITranscriptStreamingRealWorldLive` | 34.13 |
| `TestCodexPlanUsageLive` | 14.48 |
| `TestCodexRuntimeAvailabilityLive` | 0.35 |
| `TestCodexTokenUsageLive` | 19.41 |
| `TestCodexTranscriptStreamNoHistoryReplayLive` | 12.49 |

## Application checks

- Workflow MCP file proof and clean AUTO-notification: passed.
- Retained chat: one authoritative tool receipt, one final answer, stable turn ID, no Agent reconstruction and reusable tmux: passed.
- Two-step workflow: passed in 194.93 seconds, with both MCP file proofs, clean final results and advancement to the second step.

HTTP checks ran on application base `8beb63b54`; the final workflow uses that base plus the isolated-fixture fix. The application pins published provider `b0f17bae049f` and mcpagent `6134b0452266`.

## Required fixes and validation

Cold launch uses the session's native `task_started` event and a pre-launch time boundary. A loading screen cannot acknowledge submission. Terminal usage reads only the bound rollout; legacy workspace lookup filters metadata before decoding unrelated histories. Retained polling leaves observed direct-tool receipts to the bridge executor, while preserving native tools and unobserved MCP tools. The retained harness uses an isolated Workflow Builder because general chat is retired; workflow fixtures have unique names across processes.

SDK unit tests, targeted SDK/agent race checks, lint, fresh approval of captured streaming and formatting output, and application harness unit tests passed. A separate module-file test removed local SDK/agent replacements and compiled the published pins successfully.

An early durable-ack attempt had a model refusal to call an advertised MCP tool; the unchanged retry and final complete SDK run passed. The initial workflow run was invalidated when its server received SIGTERM and another server took the ports with a different workspace root. The final workflow uses dynamically assigned ports and verifies its health root. No required assertion, timeout or gate was weakened. The version claim is promoted only after all required checks pass.

Live cross-account, other-provider, Linux and Windows matrices are outside this receipt.

For a full repeat, launch an isolated server/workspace on unused ports and run [the P0 runner](../../scripts/run-coding-cli-p0.sh) with `--providers codex-cli --update-certified-versions`, `CODEX_CLI_WORKFLOW_P0_MODEL=gpt-6.1-sol`, the isolated workspace path and the runner's server/workspace URL overrides.
