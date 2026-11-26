# Go Provider API

The Go module is the repository's core integration surface. It gives host
applications one set of model interfaces for direct APIs, cloud platforms,
media providers, and local coding-agent CLIs. MCP Agent and MCP Agent Builder
are existing downstream consumers, but the module is not limited to those
projects.

The module path intentionally remains:

```bash
go get github.com/manishiitg/multi-llm-provider-go@latest
```

## Provider Families

API and cloud providers:

- AWS Bedrock
- OpenAI
- Anthropic
- OpenRouter
- Google Vertex AI
- Azure AI
- Z.AI
- Kimi
- MiniMax and MiniMax Coding Plan

Coding-agent adapters:

- Claude Code
- Codex CLI
- Cursor Agent
- Pi CLI

Gemini CLI was removed. Use Pi CLI for Gemini models or Vertex for direct
Gemini API access.

Media providers:

- ElevenLabs
- Deepgram

## Capabilities

Depending on the provider, the common interfaces cover:

- Text generation and streaming
- Tool calling
- Structured output
- Token usage and model metadata
- Embeddings
- Image input and image generation
- Video generation and conversational video editing
- Audio generation and transcription
- Music generation
- CLI-native coding-agent execution

## Basic Initialization

```go
model, err := llmproviders.InitializeLLM(llmproviders.Config{
    Provider: llmproviders.ProviderOpenAI,
    ModelID:  "gpt-4.1-mini",
})
if err != nil {
    return err
}
```

See `.env.example`, package documentation, and the adapter tests for the
provider-specific configuration currently supported.

## Transport Choice

API and cloud providers use their provider SDK or normal HTTP transport. They
are appropriate for conventional request/response applications and hosted
model capabilities.

Coding-agent providers use installed native CLIs. Their default interactive
transport runs in tmux so the library can preserve session state, stream
terminal progress, send follow-up input, interrupt work, and recover native
sessions. Provider-specific call options select working directories, models,
approval policies, tools, and continuation behavior.

The optional `llm-provider-mcp` command builds an asynchronous MCP delegation
surface on top of these coding-agent adapters. Applications can use the Go API
directly without installing or running the MCP server.

## Stability Policy

- Exported APIs used by MCP Agent and MCP Agent Builder are protected by
  downstream compile checks in CI.
- Reusable model, transport, and provider behavior belongs in the Go module.
- MCP-specific job lifecycle and tool schemas belong in the MCP packages.
- Deprecated exported APIs are removed only in a documented breaking release.
- The stable module path is
  `github.com/manishiitg/multi-llm-provider-go`.

## Testing

```bash
go test -p 1 ./...
make build
./bin/llm-test --help
```

Real provider tests require the corresponding native CLI or API credentials and
are opt-in. Replay and contract tests remain the normal CI path. See the
[API provider test contract](api_provider_test_contract.md) and
[coding-agent tmux contract](coding_sdk_tmux_contract.md) for the detailed
coverage matrices.
