# Token Usage Tracking

## Overview

The token usage tracking feature automatically records and persists LLM token consumption across all agents in a workflow iteration. Token counts are stored in **millions** for easier cost calculations (pricing is typically per million tokens).

## File Location

Token usage data is persisted to:
```
runs/{iteration-folder}/token_usage.json
```

## Data Structure

The `token_usage.json` file contains three main sections:

### 1. `by_model` - Token Usage by LLM Model
Aggregates tokens per model across all steps:
```json
{
  "by_model": {
    "claude-3-5-sonnet-20241022": {
      "provider": "anthropic",
      "prompt_tokens": 0.411,      // in millions
      "completion_tokens": 0.040,   // in millions
      "total_tokens": 0.451,        // in millions
      "cache_tokens": 0.120,
      "reasoning_tokens": 0.0,
      "llm_call_count": 15
    }
  }
}
```

### 2. `by_step` - Token Usage by Individual Step
Tracks tokens for each workflow step:
```json
{
  "by_step": {
    "execution:0": {
      "step_type": "execution",
      "step_title": "Extract data from API",
      "prompt_tokens": 0.05,
      "completion_tokens": 0.015,
      "total_tokens": 0.065,
      "cache_tokens": 0.01,
      "reasoning_tokens": 0.0,
      "llm_call_count": 3
    }
  }
}
```

### 3. `by_step_type` - Aggregated Token Usage by Step Type
Sums tokens across all steps of the same type:
```json
{
  "by_step_type": {
    "execution": {
      "step_type": "execution",
      "prompt_tokens": 0.5,      // Total across all execution steps
      "completion_tokens": 0.2,  // Total across all execution steps
      "total_tokens": 0.7,       // Total across all execution steps
      "cache_tokens": 0.1,
      "reasoning_tokens": 0.0,
      "llm_call_count": 25
    },
    "validation": {
      "step_type": "validation",
      "total_tokens": 0.4,
      ...
    },
    "learning": {
      "step_type": "learning",
      "total_tokens": 0.15,
      ...
    }
  }
}
```

## Step Types

- **execution**: Main workflow execution steps
- **validation**: Validation and verification steps
- **learning**: Learning and improvement steps

## Real-Time Persistence

Token usage is persisted **immediately** when each `token_usage` event is received, ensuring up-to-date data throughout the workflow execution.

## Cost Calculation

Since tokens are stored in millions, cost calculation is straightforward:
```javascript
const cost = usage.total_tokens * pricePerMillionTokens;
```

## Example Use Cases

- **Cost Analysis**: Calculate total cost per model or step type
- **Optimization**: Identify which step types consume the most tokens
- **Budgeting**: Track token usage across iterations
- **Debugging**: Analyze token consumption patterns

