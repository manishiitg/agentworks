package picli

import "github.com/manishiitg/multi-llm-provider-go/llmtypes"

const (
	ModelGemini4Hydrogen    = "google/gemini-4-hydrogen"
	ModelGemini4Neon        = "google/gemini-4-neon"
	ModelGemini4Argon       = "google/gemini-4-argon"
	ModelGemini4Carbon      = "google/gemini-4-carbon"
	ModelGemini35FlashLite  = "google/gemini-3.5-flash-lite"
	ModelGemini31ProPreview = "google/gemini-3.1-pro-preview"
	ModelMiniMaxM3          = "minimax/MiniMax-M3"
	ModelGLM53              = "zai/glm-5.3"
	ModelGLM53Flash         = "zai/glm-5.3-flash"
	ModelKimiK3             = "moonshotai/kimi-k3"
)

var knownPiCLIModels = []string{
	DefaultModelID,
	ModelGemini4Hydrogen,
	ModelGemini4Neon,
	ModelGemini4Argon,
	ModelGemini4Carbon,
	ModelGemini35FlashLite,
	ModelGemini31ProPreview,
	ModelMiniMaxM3,
	ModelGLM53,
	ModelGLM53Flash,
	ModelKimiK3,
}

// GetAllPiCLIModels returns the frontend-visible Pi CLI routed model selectors.
func GetAllPiCLIModels() []*llmtypes.ModelMetadata {
	models := make([]*llmtypes.ModelMetadata, 0, len(knownPiCLIModels))
	adapter := &PiCLIAdapter{}

	for _, modelID := range knownPiCLIModels {
		meta, err := adapter.GetModelMetadata(modelID)
		if err != nil || meta == nil {
			continue
		}

		switch modelID {
		case DefaultModelID:
			meta.ModelName = "Pi CLI (Gemini 3.8 Flash)"
		case ModelGemini4Hydrogen:
			meta.ModelName = "Pi CLI (Gemini 4 Hydrogen)"
		case ModelGemini4Neon:
			meta.ModelName = "Pi CLI (Gemini 4 Neon)"
		case ModelGemini4Argon:
			meta.ModelName = "Pi CLI (Gemini 4 Argon)"
		case ModelGemini4Carbon:
			meta.ModelName = "Pi CLI (Gemini 4 Carbon)"
		case ModelGemini35FlashLite:
			meta.ModelName = "Pi CLI (Gemini 3.5 Flash-Lite)"
		case ModelGemini31ProPreview:
			meta.ModelName = "Pi CLI (Gemini 3.1 Pro Preview)"
		case ModelMiniMaxM3:
			// 1M-token context, same as the adapter default -- no override needed.
			meta.ModelName = "Pi CLI (MiniMax M3)"
		case ModelGLM53:
			// 1M-token context, same as the adapter default -- no override needed.
			meta.ModelName = "Pi CLI (GLM 5.3)"
		case ModelGLM53Flash:
			meta.ModelName = "Pi CLI (GLM 5.3 Flash)"
		case ModelKimiK3:
			// 1M-token context, same as the adapter default -- no override needed.
			meta.ModelName = "Pi CLI (Kimi K3)"
		}
		meta.ModelSelectionMode = "dynamic"

		models = append(models, meta)
	}

	// A deployment's own Pi providers (PI_CLI_AGENT_TEMPLATE_DIR) are real choices on that server: list them, so
	// catalogs built from this list (model pickers, "is this model offered" checks) accept them. A broken template
	// lists nothing extra; launching then reports the template error.
	if tmpl, err := LoadPiAgentTemplate(); err == nil && tmpl != nil {
		for _, model := range tmpl.Models {
			name := model.Name
			if name == "" {
				name = model.ModelID()
			}
			meta := &llmtypes.ModelMetadata{
				ModelID:                 model.ModelID(),
				Provider:                "pi-cli",
				ModelName:               "Pi CLI (" + name + ")",
				ContextWindow:           model.ContextWindow,
				SupportsToolCalls:       true,
				SupportsReasoningEffort: true,
				ReasoningEffortLevels:   []string{"low", "medium", "high", "xhigh"},
				ModelSelectionMode:      "dynamic",
			}
			// A model the deployment pins to "off" offers no reasoning levels: choosing one would only make the gateway fail.
			if pinned, ok := tmpl.PinnedThinkingLevel("", model.ModelID()); ok && pinned == "off" {
				meta.SupportsReasoningEffort, meta.ReasoningEffortLevels = false, nil
			}
			models = append(models, meta)
		}
	}
	return models
}
