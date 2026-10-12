package server

import (
	"log"
	"os"
	"strings"
	"sync"

	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/picli"
)

// A deployment's own Pi providers (PI_CLI_AGENT_TEMPLATE_DIR, staged into
// every Pi session by the adapter; a customer's own AI gateway, for example).
// Pi's --list-models cannot show them (it runs without the provider key), so
// the picker and the server account's status read the template here.

var piTemplateLogOnce sync.Once

func deploymentPiTemplate() *picli.PiAgentTemplate {
	tmpl, err := picli.LoadPiAgentTemplate()
	if err != nil {
		piTemplateLogOnce.Do(func() { log.Printf("[PI] deployment Pi template ignored: %v", err) })
		return nil
	}
	return tmpl
}

// deploymentPiModels lists the template's models, the first as the default.
// keyedOnly keeps only models whose provider key is in the server environment.
func deploymentPiModels(keyedOnly bool) []dynamicModelEntry {
	tmpl := deploymentPiTemplate()
	if tmpl == nil {
		return nil
	}
	var entries []dynamicModelEntry
	for _, model := range tmpl.Models {
		if keyedOnly && strings.TrimSpace(os.Getenv(model.KeyEnv)) == "" {
			continue
		}
		name := strings.TrimSpace(model.Name)
		if name == "" {
			name = piModelDisplayName(model.Provider, model.ID)
		}
		entries = append(entries, dynamicModelEntry{
			ModelID:       model.ModelID(),
			ModelName:     name,
			Group:         model.Provider,
			IsDefault:     len(entries) == 0,
			ContextWindow: model.ContextWindow,
		})
	}
	return entries
}

// withDeploymentPiModels: a deployment that supplies its own Pi models offers
// only those (the first is the default). Pi's built-in catalog (Gemini, GLM,
// OpenRouter and the rest) has no key on such a deployment, so listing it only
// lets a project save a model that fails with "No API key found".
func withDeploymentPiModels(models []dynamicModelEntry, keyedOnly bool) []dynamicModelEntry {
	if deployment := deploymentPiModels(keyedOnly); len(deployment) > 0 {
		return deployment
	}
	return models
}

// deploymentPiKeyConfigured: the server holds the key of a template provider.
func deploymentPiKeyConfigured() bool {
	return len(deploymentPiModels(true)) > 0
}
