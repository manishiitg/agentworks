package events

import (
	"errors"
	"strings"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmerrors"
)

// SetProviderFailureDisplay keeps failed-turn policy on the server. Metadata is
// also carried by stored/resumed completion events, not just the live banner.
func (e *UnifiedCompletionEvent) SetProviderFailureDisplay() {
	// A turn the person stopped is not a provider failure (server B 2026-10-28: Stop showed "The provider could
	// not complete this response").
	if e.Error == "" || strings.Contains(e.Error, "context canceled") {
		return
	}
	e.SetProviderFailure(errors.New(e.Error))
}

// SetProviderFailure keeps typed quota metadata before the event becomes JSON.
func (e *UnifiedCompletionEvent) SetProviderFailure(err error) {
	if err == nil || strings.Contains(err.Error(), "context canceled") {
		return
	}
	if e.Metadata == nil {
		e.Metadata = make(map[string]interface{})
	}
	for key, value := range ProviderFailureMetadata(err) {
		e.Metadata[key] = value
	}
}

// ProviderFailureMetadata carries the provider-stated reset, never a time parsed
// from diagnostic prose. The client formats retry_at in the person's timezone.
func ProviderFailureMetadata(err error) map[string]interface{} {
	d := llmerrors.ProviderDisplay(err)
	metadata := map[string]interface{}{}
	metadata["error_code"] = d.Code
	metadata["provider_message"] = d.Message
	metadata["next_step"] = d.NextStep
	metadata["retryable"] = d.Retryable
	if llmerrors.KindOf(err) == llmerrors.KindQuotaExhausted {
		if reset := llmerrors.RetryAtOrZero(err); !reset.IsZero() {
			metadata["retry_at"] = reset.Format(time.RFC3339)
		}
	}
	return metadata
}
