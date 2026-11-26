# Custom Logger Example

This example demonstrates how to implement and use a custom logger that writes logs to a file.

## Overview

The example shows:
- How to implement the `interfaces.Logger` interface
- Creating a file-based logger that writes to `test.log`
- Using the custom logger with the LLM provider
- Thread-safe logging with proper file handling

## Prerequisites

1. **Go 1.21+** installed
2. **OpenAI API Key** - Get one from [OpenAI Platform](https://platform.openai.com/api-keys)

## Setup

1. Set your OpenAI API key:
```bash
export OPENAI_API_KEY=your-api-key-here
```

2. Navigate to the custom_logger directory:
```bash
cd examples/custom_logger
```

3. Run the example:
```bash
go run custom_logger.go
```

## What it does

1. **Creates a FileLogger**: Implements the `Logger` interface to write logs to `test.log`
2. **Initializes LLM**: Uses the custom logger with the OpenAI provider
3. **Makes a request**: Sends a message and logs all operations
4. **Displays results**: Shows the response and token usage
5. **Logs everything**: All operations are logged to `test.log` with timestamps

## Key Features

### FileLogger Implementation

The `FileLogger` struct:
- Implements `Infof`, `Errorf`, and `Debugf` methods
- Writes logs with timestamps and log levels
- Uses mutex for thread-safe logging
- Handles file errors gracefully

### Log Format

Each log entry includes:
- **Timestamp**: `2006-01-02 15:04:05` format
- **Log Level**: `INFO`, `ERROR`, or `DEBUG`
- **Message**: The formatted log message

Example log entry:
```
[2025-01-27 14:30:25] [INFO] Starting custom logger example
[2025-01-27 14:30:25] [INFO] Initializing OpenAI provider with model: gpt-4.1-mini
[2025-01-27 14:30:26] [INFO] Successfully initialized LLM
```

## Output

After running, you'll see:
1. Console output with the response and token usage
2. A `test.log` file in the same directory with detailed logs

## Customization

You can customize the logger by:
- Changing the log file name
- Adding different log levels
- Formatting timestamps differently
- Adding log rotation
- Writing to multiple files or destinations

## See Also

- [OpenAI Simple Example](../openai/) - Basic example without custom logger
- Main [README.md](../../README.md) - Full library documentation
- [TESTING.md](../../docs/TESTING.md) - Comprehensive usage examples

