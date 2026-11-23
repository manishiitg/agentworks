# Custom Event Emitter Example

This example demonstrates how to implement and use a custom event emitter that captures and logs LLM events to a file.

## Overview

The example shows:
- How to implement the `interfaces.EventEmitter` interface
- Creating a file-based event emitter that writes to `events.log`
- Using the custom event emitter with the LLM provider
- Capturing all LLM lifecycle events (initialization, generation, errors)

## Prerequisites

1. **Go 1.21+** installed
2. **OpenAI API Key** - Get one from [OpenAI Platform](https://platform.openai.com/api-keys)

## Setup

1. Set your OpenAI API key:
```bash
export OPENAI_API_KEY=your-api-key-here
```

2. Navigate to the custom_event_emitter directory:
```bash
cd examples/custom_event_emitter
```

3. Run the example:
```bash
go run custom_event_emitter.go
```

## What it does

1. **Creates a FileEventEmitter**: Implements the `EventEmitter` interface to write events to `events.log`
2. **Initializes LLM**: Uses the custom event emitter with the OpenAI provider
3. **Makes a request**: Sends a message and captures all events
4. **Displays results**: Shows the response and token usage
5. **Logs all events**: All LLM events are captured in `events.log` with detailed JSON formatting

## Key Features

### FileEventEmitter Implementation

The `FileEventEmitter` struct:
- Implements all 6 event emitter methods:
  - `EmitLLMInitializationStart`
  - `EmitLLMInitializationSuccess`
  - `EmitLLMInitializationError`
  - `EmitLLMGenerationSuccess`
  - `EmitLLMGenerationError`
  - `EmitToolCallDetected` (new!)
- Writes events with timestamps
- Formats events as JSON for readability
- Uses mutex for thread-safe event writing
- Handles file errors gracefully

### Event Types Captured

1. **LLM_INITIALIZATION_START**: When LLM initialization begins
   - Includes: provider, model_id, temperature, trace_id, metadata

2. **LLM_INITIALIZATION_SUCCESS**: When LLM initialization completes successfully
   - Includes: provider, model_id, capabilities, trace_id, metadata

3. **LLM_INITIALIZATION_ERROR**: When LLM initialization fails
   - Includes: provider, model_id, operation, error message, trace_id, metadata

4. **LLM_GENERATION_SUCCESS**: When content generation succeeds
   - Includes: provider, model_id, operation, messages count, temperature, message content, response length, choices count, trace_id, metadata

5. **LLM_GENERATION_ERROR**: When content generation fails
   - Includes: provider, model_id, operation, messages count, temperature, message content, error message, trace_id, metadata

6. **TOOL_CALL_DETECTED**: When a tool/function call is detected in the LLM response
   - Includes: provider, model_id, tool_call_id, tool_name, arguments, trace_id, metadata

### Event Format

Each event entry includes:
- **Timestamp**: `2006-01-02 15:04:05` format
- **Event Type**: The type of event (e.g., `INIT_START`, `GENERATION_SUCCESS`)
- **Event Data**: JSON-formatted event details

Example event entries:

**Initialization Event:**
```json
[2025-01-27 14:30:25] [EVENT: INIT_START]
{
  "event_type": "LLM_INITIALIZATION_START",
  "provider": "openai",
  "model_id": "gpt-4.1-mini",
  "temperature": 0.7,
  "trace_id": "",
  "metadata": {
    "model_version": "gpt-4.1-mini",
    "max_tokens": 0,
    "top_p": 0.7,
    "user": "openai_user",
    "custom_fields": {
      "provider": "openai",
      "operation": "llm_initialization"
    }
  }
}
```

**Tool Call Event:**
```json
[2025-01-27 14:30:26] [EVENT: TOOL_CALL_DETECTED]
{
  "event_type": "TOOL_CALL_DETECTED",
  "provider": "openai",
  "model_id": "gpt-4.1-mini",
  "tool_call_id": "call_abc123",
  "tool_name": "get_weather",
  "arguments": "{\"location\": \"San Francisco\"}",
  "trace_id": "",
  "metadata": {
    "user": "tool_call_user",
    "custom_fields": {
      "provider": "openai",
      "model_id": "gpt-4.1-mini",
      "tool_call_id": "call_abc123",
      "tool_type": "function",
      "tool_name": "get_weather"
    }
  }
}
```

## Output

After running, you'll see:
1. Console output with the response and token usage
2. An `events.log` file in the same directory with all captured events in JSON format

## Use Cases

Custom event emitters are useful for:
- **Observability**: Track all LLM operations for monitoring and debugging
- **Analytics**: Collect metrics on LLM usage, token consumption, and performance
- **Audit Logging**: Maintain a record of all LLM interactions
- **Integration**: Send events to external systems (databases, message queues, monitoring tools)
- **Cost Tracking**: Monitor token usage and costs across different models

## Customization

You can customize the event emitter by:
- Changing the event file name or destination
- Sending events to multiple destinations (file + database + API)
- Filtering or transforming events before writing
- Adding event buffering for better performance
- Implementing event rotation for large-scale deployments

## See Also

- [Custom Logger Example](../custom_logger/) - Example with custom logger
- [OpenAI Simple Example](../openai/) - Basic example without custom components
- Main [README.md](../../README.md) - Full library documentation
- [TESTING.md](../../docs/TESTING.md) - Comprehensive usage examples

