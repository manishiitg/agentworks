package server

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
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
//
// A person may have their own list for a shared server account (users.json
// account_allowed_models, PLAT-714): it replaces the account's list for them,
// and ["*"] means every model for them. The person is the one the turn's
// token limits count toward (tokenLimitOwnerForScope): the signed-in person,
// or for a Slack channel bot turn the owner it is billed to.

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

// allModelsMarker in a person's override means every model for them.
const allModelsMarker = "*"

// normalizePersonAllowedModels is a person's override as stored: nil (no
// override: the account's list applies), ["*"] (every model), or a list.
func normalizePersonAllowedModels(models []string) ([]string, error) {
	if containsFold(models, allModelsMarker) {
		return []string{allModelsMarker}, nil
	}
	return validateAllowedModels(models)
}

// effectiveAllowedModels is a person's list on a shared account: their
// override when they have one ("*" = every model, nil), else the account's.
func effectiveAllowedModels(account, override []string) []string {
	switch {
	case len(override) == 0:
		return account
	case containsFold(override, allModelsMarker):
		return nil
	}
	return override
}

type modelLimitPersonKey struct{}

// withModelLimitPerson names the person whose allowed models a model
// resolution under ctx uses, where the caller knows it better than ctx.
func withModelLimitPerson(ctx context.Context, person string) context.Context {
	if strings.TrimSpace(person) == "" {
		return ctx
	}
	return context.WithValue(ctx, modelLimitPersonKey{}, strings.TrimSpace(person))
}

// modelLimitPerson is the person a shared account's allowed models are read
// for: the one named by withModelLimitPerson, else the signed-in principal
// of ctx (a Slack channel bot turn resolves to the owner it is billed to,
// like its token limits). "" when ctx names nobody: the account's list.
func modelLimitPerson(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if person, ok := ctx.Value(modelLimitPersonKey{}).(string); ok && person != "" {
		return person
	}
	principal := ""
	if claims := GetUserFromContext(ctx); claims != nil {
		principal = strings.TrimSpace(claims.UserID)
	}
	if principal == "" {
		if id, ok := ctx.Value(common.UserIDKey).(string); ok {
			principal = strings.TrimSpace(id)
		}
	}
	if principal == "" {
		return ""
	}
	return tokenLimitOwnerForScope(ctx, providerAccountScope{Principal: principal})
}

// personAccountAllowedModels is the person's override for provider's shared
// account (nil when none, or the person is unknown).
func personAccountAllowedModels(person, provider string) []string {
	if strings.TrimSpace(person) == "" {
		return nil
	}
	rec := directoryUserFor(person, "", "")
	if rec == nil {
		return nil
	}
	return rec.AccountAllowedModels[provider]
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
// provider's server account, whose list is the effective one of the person
// modelLimitPerson(ctx) names. A list is a plain lookup: who may use the
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
		return effectiveAllowedModels(settings.AllowedModels[provider], personAccountAllowedModels(modelLimitPerson(ctx), provider)), adminManagedProviderAccountName, nil
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

// constrainProductChatModel applies the account's list to a product chat turn (Crew, Code, Goals, ...) before its runtime is bound. A model the
// account does not allow runs on the first allowed one instead: the turn never fails. It used to refuse a model that differed from the conversation's
// bound one, but after one fallback the conversation is bound to the allowed model while the browser keeps re-sending the saved one, so every later message
// was refused with a 422 (Code on Excellence, 2026-10-04). The pickers only offer allowed models, so a refusal protected nothing the fallback does not.
func constrainProductChatModel(ctx context.Context, query *QueryRequest) error {
	if strings.TrimSpace(query.Provider) == "" {
		return nil
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

// applyAccountAllowedModels merges an admin's per-account model overrides
// into rec: each named account's override is replaced; null or [] removes it.
func applyAccountAllowedModels(rec *UserRecord, requested map[string][]string) error {
	if len(requested) == 0 {
		return nil
	}
	next := map[string][]string{}
	for provider, models := range rec.AccountAllowedModels {
		next[provider] = models
	}
	for provider, models := range requested {
		provider = strings.TrimSpace(provider)
		if !validTokenLimitAccount(provider) {
			return fmt.Errorf("unknown shared account %q", provider)
		}
		normalized, err := normalizePersonAllowedModels(models)
		if err != nil {
			return err
		}
		if len(normalized) == 0 {
			delete(next, provider)
			continue
		}
		next[provider] = normalized
	}
	if len(next) == 0 {
		next = nil
	}
	rec.AccountAllowedModels = next
	return nil
}

// accountAllowedModelsSummary is a log line like "codex-cli=gpt-6|gpt-6-luna".
func accountAllowedModelsSummary(in map[string][]string) string {
	if len(in) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(in))
	for provider, models := range in {
		parts = append(parts, provider+"="+strings.Join(models, "|"))
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

// knownProviderModelIDs is the provider's model catalog as the pickers list
// it (dynamic providers from their live list, others from the metadata
// registry); empty when the server knows none.
func knownProviderModelIDs(provider string) []string {
	if providerModelSelectionMode(provider) == "dynamic" {
		var ids []string
		if resp := getDynamicModels(provider, true, false); resp != nil {
			for _, model := range resp.Models {
				ids = append(ids, model.ModelID)
			}
		}
		return ids
	}
	return providerModelIDs(provider)
}

// checkKnownModels refuses a model id the provider's catalog does not list,
// when the provider has a catalog. "*" always passes.
func checkKnownModels(provider string, models []string) error {
	catalog := knownProviderModelIDs(provider)
	if len(catalog) == 0 {
		return nil
	}
	var unknown []string
	for _, model := range models {
		if model != allModelsMarker && !containsFold(catalog, model) {
			unknown = append(unknown, model)
		}
	}
	if len(unknown) > 0 {
		return fmt.Errorf("unknown %s model(s): %s; known: %s", provider, strings.Join(unknown, ", "), strings.Join(catalog, ", "))
	}
	return nil
}
