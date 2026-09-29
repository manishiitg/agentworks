package server

import (
	"context"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	llm "github.com/manishiitg/multi-llm-provider-go"
)

// Without a saved default (installation or admin), a product starts with a
// provider that works for everyone there: the first, in the product's own
// order, whose shared server account is signed in (or has a key) and is
// available to everyone in that product. Nothing is hard-coded: Excellence
// with only Muse signed in starts new Codes on Muse.

// providerReadyForEveryone reports whether provider's shared account can run
// for any signed-in user of product.
var providerReadyForEveryone = func(ctx context.Context, provider, product string) bool {
	if !providerEnabled(provider) {
		return false
	}
	if configured, _ := providerAuthConfigured(provider, MergedProviderAPIKeys(ctx)); !configured {
		return false
	}
	availability, err := effectiveServerAccountAvailability(ctx, provider)
	if err != nil {
		return false
	}
	return availability.AvailableTo.All || availability.AvailableTo.admitsProduct(product)
}

// dynamicDefaultProviderOption picks the option to start with: the profile's
// own default when it is ready, else the first ready option. ok is false when
// none is ready (the profile is left as it is).
func dynamicDefaultProviderOption(ctx context.Context, product string, options []agentprofiles.ProviderOption) (agentprofiles.ProviderOption, bool) {
	for _, option := range options {
		if option.Default && providerReadyForEveryone(ctx, option.Provider, product) {
			return option, true
		}
	}
	for _, option := range options {
		if providerReadyForEveryone(ctx, option.Provider, product) {
			return option, true
		}
	}
	return agentprofiles.ProviderOption{}, false
}

// applyDynamicProductDefault marks the ready option as the profile's default
// when no default is saved for its product.
func applyDynamicProductDefault(ctx context.Context, profile *agentprofiles.Profile) {
	if profile == nil {
		return
	}
	product := strings.ToLower(strings.TrimSpace(profile.Product))
	option, ok := dynamicDefaultProviderOption(ctx, product, profile.Runtime.ProviderOptions)
	if !ok || option.Default {
		return
	}
	options := append([]agentprofiles.ProviderOption(nil), profile.Runtime.ProviderOptions...)
	profile.Runtime.ProviderOptions = options
	applyProductDefaultToProfile(profile, productDefault{Provider: option.Provider, Model: option.ModelID}, false)
}

// dynamicWorkflowDefault is the provider and model a new workflow starts with
// when no workflows default is saved.
func dynamicWorkflowDefault(ctx context.Context) (productDefault, bool) {
	for _, provider := range []string{"claude-code", "codex-cli", "cursor-cli", "muse-cli", "pi-cli", "agy-cli"} {
		if !providerReadyForEveryone(ctx, provider, productWorkflows) {
			continue
		}
		model := llm.GetDefaultModel(llm.Provider(provider))
		if model == "" {
			if opts := discoveryModelOptions(provider); len(opts) > 0 {
				model = opts[0]
			}
		}
		if model != "" {
			return productDefault{Provider: provider, Model: model}, true
		}
	}
	return productDefault{}, false
}
