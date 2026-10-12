package agent

import "github.com/manishiitg/multi-llm-provider-go/pkg/adapters/picli"

// deploymentPiModel keeps a Pi turn on a model the deployment can run. A deployment that supplies its own Pi models
// (PI_CLI_AGENT_TEMPLATE_DIR) has a key for those only; a project that saved another model (Pi's built-in default is a
// Google model) would fail with "No API key found". It returns the deployment's default model and true in that case.
func deploymentPiModel(provider, modelID string) (string, bool) {
	if provider != "pi-cli" {
		return modelID, false
	}
	tmpl, err := picli.LoadPiAgentTemplate()
	if err != nil || tmpl == nil || len(tmpl.Models) == 0 {
		return modelID, false
	}
	for _, m := range tmpl.Models {
		if m.ModelID() == modelID {
			return modelID, false
		}
	}
	return tmpl.Models[0].ModelID(), true
}
