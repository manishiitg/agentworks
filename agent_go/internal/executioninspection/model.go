package executioninspection

import (
	"encoding/json"
	"sort"
	"strings"

	orchevents "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"
)

type ModelUse struct {
	Provider string `json:"provider,omitempty"`
	ModelID  string `json:"model_id,omitempty"`
	Label    string `json:"label"`
}

type ModelInfo struct {
	ModelUse
	Previous []ModelUse `json:"previous,omitempty"`
	Tooltip  string     `json:"tooltip,omitempty"`
}

// Model reads only the displayed execution's saved evidence. Completed provider
// calls win over a header; current workflow preferences are never consulted.
func Model(transcript *orchevents.BackgroundAgentTranscript, linked []orchevents.BackgroundAgentTranscriptEvent) ModelInfo {
	events := append([]orchevents.BackgroundAgentTranscriptEvent{}, transcript.Events...)
	events = append(events, linked...)
	sort.SliceStable(events, func(i, j int) bool { return events[i].Timestamp.Before(events[j].Timestamp) })
	uses := []ModelUse{}
	for _, event := range events {
		if event.Type != "execution_model" {
			continue
		}
		var recorded orchevents.ExecutionModel
		if json.Unmarshal([]byte(event.Text), &recorded) != nil {
			continue
		}
		model, ok := modelUse(recorded.Provider, recorded.ModelID)
		if !ok {
			continue
		}
		if len(uses) == 0 || uses[len(uses)-1] != model {
			uses = append(uses, model)
		}
	}
	if len(uses) == 0 {
		if model, ok := modelUse(transcript.Provider, transcript.ModelID); ok {
			uses = append(uses, model)
		}
	}
	if len(uses) == 0 {
		return ModelInfo{ModelUse: ModelUse{Label: "Model not recorded"}}
	}
	info := ModelInfo{ModelUse: uses[len(uses)-1], Previous: uses[:len(uses)-1]}
	if len(info.Previous) > 0 {
		labels := make([]string, 0, len(info.Previous))
		for _, model := range info.Previous {
			labels = append(labels, model.Label)
		}
		info.Tooltip = "Previously used: " + strings.Join(labels, " → ")
	}
	return info
}

func modelUse(provider, id string) (ModelUse, bool) {
	provider, id = strings.TrimSpace(provider), strings.TrimSpace(id)
	if provider == "" || id == "" {
		return ModelUse{}, false
	}
	// Only strip the matching transport prefix; vendor-qualified API IDs survive.
	id = strings.TrimPrefix(id, provider+"/")
	if id == "" {
		return ModelUse{}, false
	}
	label := map[string]string{
		"codex-cli": "Codex", "claude-code": "Claude", "muse-cli": "Muse",
		"agy-cli": "Antigravity", "pi-cli": "Pi", "cursor": "Cursor", "cursor-cli": "Cursor",
		"openai": "OpenAI", "anthropic": "Anthropic", "openrouter": "OpenRouter", "bedrock": "Amazon Bedrock",
		"gemini": "Gemini", "vertex": "Vertex AI", "azure": "Azure OpenAI", "ollama": "Ollama",
	}[provider]
	if label == "" {
		label = provider
	}
	return ModelUse{Provider: provider, ModelID: id, Label: label + " · " + id}, true
}
