# LLM resilience: retries on the selected model

MCP Agent executes one configured model. It never switches models, coding agents, or providers after a failure, including during initialization.

Transient throttling, connection, stream, and internal errors use bounded exponential backoff on that same model. Empty-content and zero-candidate failures have shorter retry budgets. Authentication errors and unavailable models return errors. Exhausted quota retains the provider's reset time and avoids further calls until the known window reopens. Cancellation and user-input-required responses stop retries immediately.

Configure `GenerationRuntimeConfig.LLM.Primary` with the selected provider, model, credentials, and options. `AgentLLMConfiguration.Fallbacks`, `llm.Config.FallbackModels`, and provider fallback lookup helpers have been removed. Legacy JSON fallback fields are ignored and are not serialized again. Provider fallback environment variables do not affect execution.

`LLM_MAX_RETRIES` controls the attempt budget (default 5). `LLM_RETRY_BASE_DELAY_SECONDS` defaults to 10 and `LLM_RETRY_MAX_DELAY_SECONDS` defaults to 300. Zero-candidate failures allow at most 3 attempts and empty-content failures at most 2, bounded by the overall budget.

The implementation is in `agent/llm_generation.go`. Events are `llm_generation_with_retry`, `retry_attempt`, and the existing error/cancellation events. Retry attempts do not mutate the agent's provider or model identity.
