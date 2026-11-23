package utils

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/pkoukk/tiktoken-go"
)

// errEncodingLoadInBackoff marks a getCachedEncoding failure as "known to
// be failing recently", so callers/tests can distinguish it from a genuinely
// new, unexpected error via errors.Is.
var errEncodingLoadInBackoff = errors.New("encoding load recently failed")

// TokenCounter provides provider/model-aware token counting using tiktoken
type TokenCounter struct {
}

var (
	encodingCache   sync.Map
	encodingCacheMu sync.Mutex
	// encodingFailureCache remembers a RECENT failure to load an encoding
	// (encodingName -> time.Time of that failure), so a persistent problem
	// costs one bounded wait, not one per call.
	//
	// Without this, a failing tiktoken.GetEncoding was retried on every single
	// call with no memory of the prior failure, each attempt serialized behind
	// encodingCacheMu (held for the full attempt). Measured live 2026-08-19: a
	// single agent turn calls CountTokensForModel repeatedly (once per content
	// string being sized -- user message, tool description, tool result, ...),
	// and with the network unable to reach tiktoken-go's CDN, each of those
	// calls queued up and paid its own ~10s bounded timeout (see
	// token_counter_bpe_loader.go) in series, compounding well past what any
	// single call's timeout suggests. The graceful len(content)/4 fallback this
	// package already has was reached every time, correctly -- it was just
	// reached slowly, over and over.
	encodingFailureCache sync.Map
)

// encodingFailureBackoff bounds how long a remembered failure is trusted
// before the next call is allowed to try the network again. Short enough that
// a transient blip does not degrade token counting to the char/4 approximation
// for the rest of a long session; long enough that a real outage costs one
// timeout per window, not one per call.
const encodingFailureBackoff = 60 * time.Second

// NewTokenCounter creates a new token counter instance
func NewTokenCounter() *TokenCounter {
	return &TokenCounter{}
}

// CountTokens counts tokens for the given content using provider/model-specific encoding
// It uses ModelMetadata to determine the correct encoding based on provider and model
func (tc *TokenCounter) CountTokens(content string, metadata *llmtypes.ModelMetadata) (int, error) {
	if metadata == nil {
		// Fallback to o200k_base if no metadata available
		return tc.countTokensWithEncoding(content, "o200k_base")
	}

	// Get encoding based on provider and model
	encoding := tc.getEncodingForModel(metadata.Provider, metadata.ModelID)
	return tc.countTokensWithEncoding(content, encoding)
}

// CountTokensForProvider counts tokens using provider name and model ID
// This is a convenience method when you don't have ModelMetadata
func (tc *TokenCounter) CountTokensForProvider(content, provider, modelID string) (int, error) {
	encoding := tc.getEncodingForModel(provider, modelID)
	return tc.countTokensWithEncoding(content, encoding)
}

// getEncodingForModel determines the correct tiktoken encoding based on provider and model
// Note: tiktoken is primarily designed for OpenAI models. For other providers, we use the closest approximation.
func (tc *TokenCounter) getEncodingForModel(provider, modelID string) string {
	provider = strings.ToLower(provider)
	modelID = strings.ToLower(modelID)

	switch provider {
	case "openai", "openrouter":
		// OpenAI (and OpenRouter when proxying OpenAI) models use different encodings
		// depending on the model family.
		//
		// Current split, based on OpenAI/tokenizer libs and tiktoken:
		//   - GPT-3.5, GPT-4, GPT-4-turbo  -> cl100k_base
		//   - GPT-4o, GPT-4.1(+ variants), o1/o3, GPT-5.x -> o200k_base
		//
		// NOTE: We intentionally branch on model ID patterns here instead of using a
		// single encoding for all OpenAI models, because newer families switched
		// to o200k_base while older ones remain on cl100k_base.

		// Normalize once more in case the caller passed mixed-case IDs
		m := modelID

		// Older GPT-3.5 / GPT-4 families → cl100k_base
		if strings.Contains(m, "gpt-3.5") {
			return "cl100k_base"
		}
		// GPT-4* but NOT 4o / 4.1 → cl100k_base (e.g. gpt-4, gpt-4-32k, gpt-4-turbo)
		if strings.HasPrefix(m, "gpt-4") &&
			!strings.HasPrefix(m, "gpt-4o") &&
			!strings.HasPrefix(m, "gpt-4.1") {
			return "cl100k_base"
		}

		// Modern OpenAI families → o200k_base
		// - gpt-4o and variants (including dated versions)
		// - gpt-4.1 and variants (mini, nano, etc.)
		// - gpt-5.x (including mini/nano/pro/etc.)
		// - o1, o3, o4 families
		if strings.HasPrefix(m, "gpt-4o") ||
			strings.HasPrefix(m, "gpt-4.1") ||
			strings.HasPrefix(m, "gpt-5") ||
			strings.HasPrefix(m, "o1") ||
			strings.HasPrefix(m, "o3") ||
			strings.HasPrefix(m, "o4") {
			return "o200k_base"
		}

		// Fallback for any other OpenAI/OpenRouter models:
		// default to the newer o200k_base encoding.
		return "o200k_base"

	case "anthropic":
		// IMPORTANT: Anthropic Claude models use their own proprietary tokenizer
		// tiktoken does NOT have a perfect encoding match for Claude models
		// cl100k_base is used as an approximation, but it may not be 100% accurate
		// Claude's tokenizer typically produces MORE tokens than cl100k_base for the same text
		// For accurate counts, consider using Anthropic's official token counting API
		// This applies to all Claude models (Claude 3, 3.5, 4, etc.)
		return "cl100k_base"

	case "vertex", "google":
		// Google/Vertex Gemini models - encoding depends on model version
		// Gemini 2.0 and newer models use o200k_base encoding
		// Gemini 1.5 and older models also use o200k_base (or similar)
		// Reference: Google's tokenizer for Gemini models
		if strings.Contains(modelID, "gemini-2") || strings.Contains(modelID, "gemini-3") || strings.Contains(modelID, "gemini-1.5") {
			return "o200k_base"
		} else if strings.Contains(modelID, "gemini") {
			// Fallback for any other Gemini models
			return "o200k_base"
		}
		// Default for Google models
		return "o200k_base"

	case "bedrock":
		// AWS Bedrock supports multiple model families
		// Determine encoding based on the underlying model
		if strings.Contains(modelID, "claude") {
			// Bedrock Claude models - same limitation as direct Anthropic
			// Uses cl100k_base as approximation
			return "cl100k_base"
		} else if strings.Contains(modelID, "gemini") {
			// Bedrock Gemini models use o200k_base
			return "o200k_base"
		} else if strings.Contains(modelID, "gpt") || strings.Contains(modelID, "j2") || strings.Contains(modelID, "jurassic") {
			// Bedrock GPT/Jurassic models use cl100k_base
			return "cl100k_base"
		} else if strings.Contains(modelID, "llama") || strings.Contains(modelID, "mistral") {
			// Bedrock Llama/Mistral models - these may vary, but cl100k_base is a reasonable approximation
			return "cl100k_base"
		}
		// Default for unknown Bedrock models
		return "o200k_base"

	default:
		// Default fallback encoding
		// o200k_base is a newer, more general encoding that works reasonably well for many models
		return "o200k_base"
	}
}

// countTokensWithEncoding counts tokens using the specified encoding
// Uses caching to avoid re-initializing encodings
func (tc *TokenCounter) countTokensWithEncoding(content string, encodingName string) (int, error) {
	encoding, err := getCachedEncoding(encodingName)
	if err != nil {
		// Fallback to character-based approximation if encoding fails
		// Rough estimation: 1 token ≈ 4 characters for English text
		return len(content) / 4, err
	}

	// Count tokens
	tokens := encoding.Encode(content, nil, nil)
	return len(tokens), nil
}

func getCachedEncoding(encodingName string) (*tiktoken.Tiktoken, error) {
	if val, exists := encodingCache.Load(encodingName); exists {
		if enc, ok := val.(*tiktoken.Tiktoken); ok {
			return enc, nil
		}
	}

	// Fast path, no lock: a remembered recent failure short-circuits straight
	// to an error (and the caller's char/4 fallback) instead of queuing behind
	// encodingCacheMu for another full network attempt.
	if val, exists := encodingFailureCache.Load(encodingName); exists {
		if failedAt, ok := val.(time.Time); ok && time.Since(failedAt) < encodingFailureBackoff {
			return nil, fmt.Errorf("encoding %q failed recently (%s ago) and is in backoff: %w", encodingName, time.Since(failedAt).Round(time.Second), errEncodingLoadInBackoff)
		}
	}

	encodingCacheMu.Lock()
	defer encodingCacheMu.Unlock()

	if val, exists := encodingCache.Load(encodingName); exists {
		if enc, ok := val.(*tiktoken.Tiktoken); ok {
			return enc, nil
		}
	}
	// Re-check backoff under the lock too: a concurrent caller may have just
	// recorded the failure this goroutine was about to attempt itself.
	if val, exists := encodingFailureCache.Load(encodingName); exists {
		if failedAt, ok := val.(time.Time); ok && time.Since(failedAt) < encodingFailureBackoff {
			return nil, fmt.Errorf("encoding %q failed recently (%s ago) and is in backoff: %w", encodingName, time.Since(failedAt).Round(time.Second), errEncodingLoadInBackoff)
		}
	}

	encoding, err := tiktoken.GetEncoding(encodingName)
	if err != nil {
		encodingFailureCache.Store(encodingName, time.Now())
		return nil, err
	}
	encodingCache.Store(encodingName, encoding)
	encodingFailureCache.Delete(encodingName)
	return encoding, nil
}

// CountTokensForModel is a convenience function that counts tokens using a Model interface
// It automatically fetches model metadata and uses the appropriate encoding
func CountTokensForModel(content string, model llmtypes.Model) (int, error) {
	tc := NewTokenCounter()

	// Get model metadata
	modelID := model.GetModelID()
	if modelID == "" {
		// Fallback if model ID is not available
		return tc.CountTokensForProvider(content, "", "")
	}

	metadata, err := model.GetModelMetadata(modelID)
	if err != nil {
		// Fallback to default encoding if metadata is unavailable
		return tc.CountTokensForProvider(content, "", modelID)
	}

	return tc.CountTokens(content, metadata)
}
