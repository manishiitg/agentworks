package server

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/llmguard"
)

// Allowed models per provider account (docs/DECISIONS.md, 2026-10-03).
//
// Every account (the installed or admin-managed "global:<provider>" account
// and every personal account) may carry a list of model ids. An empty list is
// the default and means every model. The list is enforced where a model is
// bound or resolved for an account, never only in the UI:
//
//   - an explicit change to a disallowed model is refused (checkAccountModel);
//   - a saved selection that names a disallowed model runs on the first
//     allowed model instead (resolveAccountModel), so an old workflow or
//     project setting never fails the chat.

const (
	maxAllowedModels      = 200
	maxAllowedModelIDSize = 200
)

// normalizeAllowedModels trims, de-duplicates (case-insensitively) and drops
// empty ids. The result is nil when no model remains, which means "all".
func normalizeAllowedModels(models []string) []string {
	var out []string
	for _, raw := range models {
		model := strings.TrimSpace(raw)
		if model == "" || containsFold(out, model) {
			continue
		}
		out = append(out, model)
	}
	return out
}

// validateAllowedModels checks a list an admin or owner submitted.
func validateAllowedModels(models []string) ([]string, error) {
	normalized := normalizeAllowedModels(models)
	if len(normalized) > maxAllowedModels {
		return nil, fmt.Errorf("allowed_models may list at most %d models", maxAllowedModels)
	}
	for _, model := range normalized {
		if len(model) > maxAllowedModelIDSize {
			return nil, fmt.Errorf("a model id in allowed_models is too long")
		}
	}
	return normalized, nil
}

// modelAllowed reports whether model may run on an account whose list is
// allowed. An empty list admits every model.
func modelAllowed(allowed []string, model string) bool {
	return len(allowed) == 0 || containsFold(allowed, strings.TrimSpace(model))
}

func disallowedModelError(model, accountName string, allowed []string) error {
	if strings.TrimSpace(accountName) == "" {
		accountName = adminManagedProviderAccountName
	}
	return fmt.Errorf("%s is not allowed on %s; allowed: %s", strings.TrimSpace(model), accountName, strings.Join(allowed, ", "))
}

// accountAllowedModels returns the allowed-model list of the account a model
// runs on and the account's display name. An empty connectionID, a
// "global:<provider>" id and the server-default marker all mean the
// provider's server account. A list is a plain lookup: who may use the
// account is decided separately by admitProviderAccount.
func accountAllowedModels(ctx context.Context, provider, connectionID string) ([]string, string, error) {
	provider, connectionID = strings.TrimSpace(provider), strings.TrimSpace(connectionID)
	if connectionID == "" || strings.HasPrefix(connectionID, "global:") || strings.HasPrefix(connectionID, llmguard.ServerDefaultConnectionPrefix) {
		if provider == "" {
			return nil, adminManagedProviderAccountName, nil
		}
		settings, err := loadProviderAccountSettings(ctx)
		if err != nil {
			return nil, adminManagedProviderAccountName, err
		}
		return settings.AllowedModels[provider], adminManagedProviderAccountName, nil
	}
	providerConnectionsMu.Lock()
	records, err := loadProviderConnections(ctx)
	providerConnectionsMu.Unlock()
	if err != nil {
		return nil, "", err
	}
	for _, record := range records {
		if record.ID == connectionID && (provider == "" || record.Provider == provider) {
			return record.AllowedModels, record.DisplayName, nil
		}
	}
	return nil, "", nil
}

// checkAccountModel refuses a model the account does not allow. An empty model
// (the provider's own default) passes: resolveAccountModel picks the model.
func checkAccountModel(ctx context.Context, provider, connectionID, model string) error {
	if strings.TrimSpace(model) == "" {
		return nil
	}
	allowed, name, err := accountAllowedModels(ctx, provider, connectionID)
	if err != nil {
		return fmt.Errorf("cannot read the allowed models of the account: %w", err)
	}
	if !modelAllowed(allowed, model) {
		return disallowedModelError(model, name, allowed)
	}
	return nil
}

// resolveAccountModel returns the model a turn runs on: model itself when the
// account allows it (or has no list), else the first allowed model. changed
// reports a substitution. A model that names nothing resolves to the first
// allowed model when the account has a list.
func resolveAccountModel(ctx context.Context, provider, connectionID, model string) (resolved string, changed bool, err error) {
	model = strings.TrimSpace(model)
	allowed, name, err := accountAllowedModels(ctx, provider, connectionID)
	if err != nil {
		return model, false, fmt.Errorf("cannot read the allowed models of the account: %w", err)
	}
	if len(allowed) == 0 || (model != "" && modelAllowed(allowed, model)) {
		return model, false, nil
	}
	if model != "" {
		log.Printf("[PROVIDER_ACCOUNT] %s: %v; running on %s instead", provider, disallowedModelError(model, name, allowed), allowed[0])
	}
	return allowed[0], true, nil
}

// constrainProductChatModel applies the account's list to a product chat turn
// (Crew, Code, Goals, ...) before its runtime is bound. A model the person
// just picked (it differs from the one the conversation is bound to) that the
// account does not allow is refused; a model carried over from the saved
// conversation or the product default runs on the first allowed one.
func constrainProductChatModel(ctx context.Context, input AgentProfileChatRequest, conversation ProductConversationRecord, query *QueryRequest) error {
	if strings.TrimSpace(query.Provider) == "" {
		return nil
	}
	picked := strings.TrimSpace(input.ModelID) != "" && !strings.EqualFold(strings.TrimSpace(input.ModelID), strings.TrimSpace(conversation.ModelID))
	if picked {
		return checkAccountModel(ctx, query.Provider, query.ConnectionID, query.ModelID)
	}
	model, changed, err := resolveAccountModel(ctx, query.Provider, query.ConnectionID, query.ModelID)
	if err != nil {
		return err
	}
	if changed {
		query.ModelID = model
		if query.LLMConfig != nil {
			configCopy := *query.LLMConfig
			configCopy.Primary.ModelID = model
			query.LLMConfig = &configCopy
		}
	}
	return nil
}

// constrainServerDefaultModels moves a product default whose model the
// provider's server account does not allow onto the first allowed model.
func constrainServerDefaultModels(defaults map[string]productDefault, settings providerAccountSettings) {
	for product, value := range defaults {
		allowed := settings.AllowedModels[value.Provider]
		if len(allowed) > 0 && !modelAllowed(allowed, value.Model) {
			value.Model = allowed[0]
			defaults[product] = value
		}
	}
}
