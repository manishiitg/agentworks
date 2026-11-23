package codingagentmodels

import (
	"fmt"
	"sort"
	"strings"

	llmproviders "github.com/manishiitg/multi-llm-provider-go"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/claudecode"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/codexcli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/cursorcli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/musecli"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/picli"
)

type Model struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	SelectionMode string `json:"selection_mode,omitempty"`
}

type Catalog struct {
	Provider        string  `json:"provider"`
	Models          []Model `json:"models"`
	LiveListCommand string  `json:"live_list_command,omitempty"`
	AcceptsCustomID bool    `json:"accepts_custom_id"`
	Note            string  `json:"note,omitempty"`
}

func List(provider string) (Catalog, error) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	var metadata []*llmtypes.ModelMetadata
	catalog := Catalog{Provider: provider}

	switch llmproviders.Provider(provider) {
	case llmproviders.ProviderCursorCLI:
		metadata = cursorcli.GetAllCursorCLIModels()
		catalog.LiveListCommand = "cursor-agent models"
		catalog.AcceptsCustomID = true
	case llmproviders.ProviderCodexCLI:
		metadata = codexcli.GetAllCodexCLIModels()
		catalog.AcceptsCustomID = true
	case llmproviders.ProviderClaudeCode:
		metadata = claudecode.GetAllClaudeCodeModels()
		catalog.AcceptsCustomID = true
	case llmproviders.ProviderPiCLI:
		metadata = picli.GetAllPiCLIModels()
		catalog.LiveListCommand = "pi --list-models"
		catalog.AcceptsCustomID = true
		catalog.Note = "Pi also accepts provider/model selectors, including openrouter/<model-id>."
	case llmproviders.ProviderMuseCLI:
		metadata = musecli.GetAllMuseCLIModels()
		catalog.AcceptsCustomID = true
	default:
		return Catalog{}, fmt.Errorf("unsupported coding-agent provider %q", provider)
	}

	catalog.Models = make([]Model, 0, len(metadata))
	for _, item := range metadata {
		if item == nil || strings.TrimSpace(item.ModelID) == "" {
			continue
		}
		catalog.Models = append(catalog.Models, Model{
			ID:            item.ModelID,
			Name:          item.ModelName,
			SelectionMode: item.ModelSelectionMode,
		})
	}
	return catalog, nil
}

func ListAll() []Catalog {
	providers := []string{
		string(llmproviders.ProviderClaudeCode),
		string(llmproviders.ProviderCodexCLI),
		string(llmproviders.ProviderCursorCLI),
		string(llmproviders.ProviderPiCLI),
		string(llmproviders.ProviderMuseCLI),
	}
	catalogs := make([]Catalog, 0, len(providers))
	for _, provider := range providers {
		catalog, err := List(provider)
		if err == nil {
			catalogs = append(catalogs, catalog)
		}
	}
	sort.Slice(catalogs, func(i, j int) bool { return catalogs[i].Provider < catalogs[j].Provider })
	return catalogs
}

func LiveCommand(provider string) (string, []string, bool) {
	switch llmproviders.Provider(strings.ToLower(strings.TrimSpace(provider))) {
	case llmproviders.ProviderCursorCLI:
		return "cursor-agent", []string{"models"}, true
	case llmproviders.ProviderPiCLI:
		return "pi", []string{"--list-models"}, true
	default:
		return "", nil, false
	}
}
