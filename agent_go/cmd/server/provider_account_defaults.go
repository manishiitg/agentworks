package server

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
)

// Product defaults (docs/design/provider_accounts.md, "Product defaults"):
// the provider and model a new workflow, Crew or Code starts with. New items
// copy the default into their own settings when created, so changing a
// default never switches an existing item.

// applyProductDefaultToProfile marks value as the profile's default engine.
// allowAdd lets the installation add an engine or model the product does not
// list (at registration, before validation); an admin default at run time
// may only pick what the product offers.
func applyProductDefaultToProfile(profile *agentprofiles.Profile, value productDefault, allowAdd bool) bool {
	if profile == nil || value.Provider == "" {
		return false
	}
	options := profile.Runtime.ProviderOptions
	index := -1
	for i, option := range options {
		if strings.EqualFold(strings.TrimSpace(option.Provider), value.Provider) {
			index = i
			break
		}
	}
	if index < 0 {
		if !allowAdd {
			return false
		}
		options = append(options, agentprofiles.ProviderOption{ID: "default-" + value.Provider, Label: providerDisplayLabel(value.Provider), Provider: value.Provider, ModelID: value.Model})
		index = len(options) - 1
	}
	option := options[index]
	if value.Model != "" && !strings.EqualFold(option.ModelID, value.Model) {
		if len(option.Models) > 0 && !containsFold(option.Models, value.Model) {
			if !allowAdd {
				return false
			}
			option.Models = append(append([]string(nil), option.Models...), value.Model)
		}
		option.ModelID = value.Model
	}
	updated := make([]agentprofiles.ProviderOption, len(options))
	copy(updated, options)
	for i := range updated {
		updated[i].Default = i == index
	}
	updated[index] = option
	updated[index].Default = true
	profile.Runtime.ProviderOptions = updated
	return true
}

// applyInstallationProductDefault applies AGENTWORKS_PRODUCT_DEFAULTS to a
// built-in profile before it is registered.
func applyInstallationProductDefault(profile *agentprofiles.Profile) {
	defaults, err := installationProductDefaults()
	if err != nil || profile == nil {
		return
	}
	value, ok := defaults[strings.ToLower(strings.TrimSpace(profile.Product))]
	if !ok {
		return
	}
	if applyProductDefaultToProfile(profile, value, true) {
		log.Printf("[PROVIDER_ACCOUNT] %s starts with %s / %s (installation default)", profile.Product, value.Provider, value.Model)
	}
}

// profileWithProductDefault marks the effective product default (an admin
// default included) on a profile served to the UI or used to create a
// project.
func profileWithProductDefault(ctx context.Context, profile agentprofiles.Profile) agentprofiles.Profile {
	defaults, err := effectiveProductDefaults(ctx)
	if value, ok := defaults[strings.ToLower(strings.TrimSpace(profile.Product))]; err == nil && ok {
		options := append([]agentprofiles.ProviderOption(nil), profile.Runtime.ProviderOptions...)
		profile.Runtime.ProviderOptions = options
		applyProductDefaultToProfile(&profile, value, false)
		return profile
	}
	// Nothing saved: start on a provider that works for everyone here.
	applyDynamicProductDefault(ctx, &profile)
	return profile
}

// productDefaultAllowedByProfile refuses an admin default a product does not
// offer (Crew and Code run only the engines their product lists).
func (api *StreamingAPI) productDefaultAllowedByProfile(product string, value productDefault) error {
	if api == nil || api.agentProfiles == nil {
		return nil
	}
	profile, err := api.agentProfiles.Resolve(product, 0, "")
	if err != nil || len(profile.Runtime.ProviderOptions) == 0 {
		return nil
	}
	for _, option := range profile.Runtime.ProviderOptions {
		if !strings.EqualFold(option.Provider, value.Provider) {
			continue
		}
		if strings.EqualFold(option.ModelID, value.Model) || len(option.Models) == 0 || containsFold(option.Models, value.Model) {
			return nil
		}
	}
	return fmt.Errorf("%s does not offer %s with model %s; pick one of its engines", productDisplayName(product), value.Provider, value.Model)
}

// productDefaultWorkflowLLMConfig is the model settings a new workflow
// copies from the workflows product default (nil without one).
func productDefaultWorkflowLLMConfig(ctx context.Context) *workflowtypes.PresetLLMConfig {
	defaults, _ := effectiveProductDefaults(ctx)
	value, ok := defaults[productWorkflows]
	if !ok || value.Provider == "" || value.Model == "" {
		// Nothing saved: a provider that works for everyone, if any.
		if value, ok = dynamicWorkflowDefault(ctx); !ok {
			return nil
		}
	}
	return &workflowtypes.PresetLLMConfig{SchemaVersion: 2, Mode: workflowtypes.LLMConfigModeExplicit, BuilderLLM: &workflowtypes.AgentLLMConfig{Provider: value.Provider, ModelID: value.Model}}
}
