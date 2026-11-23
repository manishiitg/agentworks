# Changelog

## Unreleased

### Removed

- **BREAKING:** Removed the Agy provider and adapter (`ProviderAgyCLI`,
  `pkg/adapters/agycli`) entirely. Any caller constructing a model with this
  provider will fail; there is no compatibility shim. If you depend on Agy,
  pin to a commit before this change rather than upgrading.

### Changed

- Updated the Pi default and workflow tiers to Gemini 3.7 Flash. Gemini 3.6
  Flash remains supported by Pi's custom model-ID path for existing
  configurations; Vertex remains on 3.6 until Google's Vertex rollout.
- Updated the Vertex and Pi defaults to Gemini 3.6 Flash, added Gemini 3.5
  Flash-Lite as the low-cost Pi tier and Vertex validation model, and omitted
  sampling parameters deprecated by both new model APIs.

## 0.7.2 - 2026-07-13

### Changed

- Made the initial host checklist explicitly explain how to move, select one or
  both hosts with Space, and confirm with Enter.

## 0.7.1 - 2026-07-13

### Fixed

- Kept interactive confirmations inside the terminal UI so setup no longer
  requires an extra Enter on terminals affected by the mixed input readers.

## 0.7.0 - 2026-07-13

### Added

- Added a real Claude Code to Codex CLI GPT-5.6 Sol delegation demo, sanitized
  transcript, GIF, and MP4.
- Added a real Codex to Cursor Agent end-to-end verification graphic.

### Changed

- Serialized tmux prompt and interrupt delivery across coding-agent adapters,
  added readiness tracking, and made persistent sessions faster and more
  resilient.
- Updated the compatibility library's default workflow builders to use Claude
  Opus 4.8 and GPT-5.6 Sol with high reasoning.

### Documentation

- Documented the Codex 0.145 rollout requirement for GPT-5.6 Sol and
  Luna, including multiple-installation troubleshooting.

### Fixed

- Fixed macOS release installs being terminated at launch because Darwin
  binaries produced on Linux had invalid ad-hoc code signatures.
- Fixed interactive setup preselecting every installed host and target, and
  prevented a host from being registered as its own delegation target.

### Breaking changes

- Removed the deprecated Gemini CLI provider, adapter package, public options,
  test commands, and compatibility contract. Use Pi CLI for Gemini models or
  Vertex for direct Gemini API access.

## 0.6.1 - 2026-07-11

### Security

- Updated the Go release toolchain floor to 1.25.12.
- Patched advisories in `golang.org/x/crypto`, `golang.org/x/net`,
  OpenTelemetry, and gRPC.
- Added reachable-code vulnerability scanning to CI.
- Replaced the Gitleaks action wrapper with a pinned CLI invocation that uses
  the repository configuration and produces SARIF output.

### Changed

- Updated GitHub Actions to current Node 24-based major versions.

### Fixed

- Replaced a fixed Pi/tmux test startup delay with a bounded readiness check.

## 0.6.0 - 2026-07-11

### Added

- Added the `llm-provider-mcp` launch documentation, real Claude-to-Cursor demo,
  social preview, security model, privacy statement, contribution guidance,
  roadmap, issue forms, and channel-specific launch kit.
- Added downstream compatibility compilation for MCP Agent and MCP Agent
  Builder.
- Added platform-specific MCP bundles and official MCP Registry publication to
  the release workflow.

### Changed

- Repositioned the repository around asynchronous coding-agent delegation.
- Renamed the GitHub product repository to `llm-provider-mcp` while retaining
  the existing Go module path for downstream compatibility.
- Split the historical provider implementation into registry, media,
  initialization, catalog, runtime, options, and management files without
  changing the package API.
- Renamed Z.AI CLI test-command source files so they are not mistaken for Go
  `_test.go` files.
- Limited the standalone scheduled security scan to schedule/manual triggers;
  push and pull-request scans remain in the main CI workflow.

### Removed

- Removed confirmed unreachable private/internal helpers and an unused Z.AI
  image fixture.

### Breaking changes

- Removed the deprecated `vertex.VertexImagenAdapter` and
  `vertex.NewVertexImagenAdapter`. Use `vertex.GeminiImageAdapter` with
  `vertex.NewGeminiImageAdapter` for Google image generation.
