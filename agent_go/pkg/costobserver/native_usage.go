package costobserver

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/costledger"
	"github.com/manishiitg/mcpagent/events"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/claudecode"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/codexcli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/musecli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/picli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/nativeusage"
)

// NativeAccounting is supplied by the hosted runtime. Standalone observers and
// unreadable native sources retain the ordinary foreground accounting path.
type NativeAccounting interface {
	Begin(costledger.Entry)
	Record(costledger.Entry) bool
}
type NativeAccountingFactory func(costledger.Entry, string, string) NativeAccounting

var nativeFactory struct {
	sync.RWMutex
	value NativeAccountingFactory
}

func SetNativeAccountingFactory(f NativeAccountingFactory) {
	nativeFactory.Lock()
	nativeFactory.value = f
	nativeFactory.Unlock()
}
func newNativeAccounting(o *Observer) NativeAccounting {
	nativeFactory.RLock()
	f := nativeFactory.value
	nativeFactory.RUnlock()
	if f == nil || o.ledger == nil {
		return nil
	}
	base := o.baseEntry(&events.AgentEvent{Timestamp: time.Now()})
	base.Provider = o.provider
	base.ModelID = o.modelID
	return f(base, o.provider, o.modelID)
}

// NativeEntry retains the server's admitted attribution, never provider-supplied
// user/product fields. All CLI price-table numbers remain estimates.
func NativeEntry(base costledger.Entry, provider string, r nativeusage.Record) costledger.Entry {
	e := base
	e.OperationMetadata = map[string]interface{}{"native_usage": true, "usage_source": "native", "usage_source_id": r.ID, "native_session_id": r.NativeSessionID, "native_turn_id": r.TurnID, "native_task_id": r.TaskID, "native_owner_type": r.OwnerType, "prompt_tokens_include_cache": r.InputIncludesCache}
	key := r.Key(provider, base.AccountID)
	sum := sha256.Sum256([]byte(key))
	e.EventID = fmt.Sprintf("native-usage-%x", sum[:20])
	e.IdempotencyKey = e.EventID
	e.Provider = provider
	e.EffectiveProvider = provider
	e.EffectiveModelID = r.Model
	e.ModelID = base.ModelID
	e.Timestamp = r.Timestamp
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now()
	}
	e.PromptTokens = int(r.Input)
	e.CompletionTokens = int(r.Output)
	e.CacheReadTokens = int(r.CacheRead)
	e.CacheWriteTokens = int(r.CacheWrite)
	e.ReasoningTokens = int(r.Reasoning)
	e.LLMCallCount = 1
	e.TurnCount = 0
	e.LLMGenerationDurationMS = 0
	if r.OwnerType == "helper" {
		e.Component = "native_helper"
	} else if r.OwnerType != "" && r.OwnerType != "main_root" {
		e.Component = "native_internal_task"
	}
	e.TotalCostUSD = 0
	e.CostUSDSource = "estimated"
	e.BillingBasis = "unpriced"
	e.PricingSource = ""
	model := strings.TrimSpace(r.Model)
	if model == "" && r.ParentModel != "" {
		model = r.ParentModel
		e.OperationMetadata["usage_rate_assumed"] = "parent"
		e.OperationMetadata["pricing_model_id"] = model
	}
	var meta *llmtypes.ModelMetadata
	var err error
	if model != "" {
		switch provider {
		case "claude-code":
			meta, err = (&claudecode.ClaudeCodeAdapter{}).GetModelMetadata(model)
		case "codex-cli":
			meta, err = (&codexcli.CodexCLIAdapter{}).GetModelMetadata(model)
		case "muse-cli":
			meta, err = musecli.GetMuseModelMetadata(model)
		case "pi-cli":
			meta, err = (&picli.PiCLIAdapter{}).GetModelMetadata(model)
		}
	}
	if err == nil && meta != nil {
		if meta.ModelID != model {
			return e
		}
		prompt, out, cache := e.PromptTokens, e.CompletionTokens, e.CacheReadTokens
		e.TotalCostUSD = llmtypes.ComputeUSDCostFromMetadata(meta, &llmtypes.GenerationInfo{PromptTokens: &prompt, CompletionTokens: &out, CachedContentTokens: &cache, Additional: map[string]interface{}{"prompt_tokens_include_cache": r.InputIncludesCache, "cache_creation_input_tokens": e.CacheWriteTokens}})
		if e.TotalCostUSD > 0 {
			e.BillingBasis = "token_estimate"
			if isSubscriptionCodingProvider(provider) {
				e.BillingBasis = "subscription_shadow"
			}
			e.PricingSource = "model_registry"
		}
	}
	return e
}

func unidentifiedAgyModel(provider, model string) bool {
	p := strings.ToLower(strings.TrimSpace(provider))
	m := strings.ToLower(strings.TrimSpace(model))
	return (p == "agy-cli" || p == "agy_cli") && (m == "" || m == "agy-cli" || m == "agy_cli")
}
