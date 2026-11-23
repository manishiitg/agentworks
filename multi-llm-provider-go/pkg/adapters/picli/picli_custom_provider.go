package picli

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// PiCustomProvider is one person's own OpenAI-compatible endpoint (a
// "bring your own key" account in AgentWorks): Pi has no built-in provider
// for it, so the adapter stages a models.json entry into that account's
// session agent dir, exactly like a deployment template (EnvPiAgentTemplateDir)
// but per account.
//
// The entry never carries the key: apiKey is "$<NAME>_API_KEY", the one
// variable the adapter itself puts the account's key in (piAPIKeyEnv), so
// the key arrives only through the scoped provider-credential path.
type PiCustomProvider struct {
	// Name is the Pi provider id (lower-case letters, digits, dashes).
	Name string
	// BaseURL is the OpenAI-compatible API root, e.g. https://host/v1.
	BaseURL string
	// Models are the model ids the endpoint serves (without the provider prefix).
	Models []string
}

// Validate checks the provider before it is written into Pi's config.
func (c *PiCustomProvider) Validate() error {
	if c == nil {
		return nil
	}
	if normalizePiProviderName(c.Name) == "" {
		return fmt.Errorf("custom Pi provider name %q must be lower-case letters, digits and dashes", c.Name)
	}
	parsed, err := url.Parse(strings.TrimSpace(c.BaseURL))
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" || parsed.User != nil {
		return fmt.Errorf("custom Pi provider %s needs an http(s) base URL", c.Name)
	}
	if len(c.Models) == 0 {
		return fmt.Errorf("custom Pi provider %s lists no models", c.Name)
	}
	for _, model := range c.Models {
		model = strings.TrimSpace(model)
		if model == "" || strings.HasPrefix(model, "!") || strings.HasPrefix(model, "$") {
			return fmt.Errorf("custom Pi provider %s has an invalid model id %q", c.Name, model)
		}
	}
	return nil
}

// providerEntry is the models.json "providers" entry for c.
func (c *PiCustomProvider) providerEntry() (json.RawMessage, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	type model struct {
		ID string `json:"id"`
	}
	models := make([]model, 0, len(c.Models))
	for _, id := range c.Models {
		models = append(models, model{ID: strings.TrimSpace(id)})
	}
	return json.Marshal(struct {
		BaseURL string  `json:"baseUrl"`
		API     string  `json:"api"`
		APIKey  string  `json:"apiKey"`
		Models  []model `json:"models"`
	}{
		BaseURL: strings.TrimRight(strings.TrimSpace(c.BaseURL), "/"),
		API:     "openai-completions",
		APIKey:  "$" + PiProviderKeyEnv(c.Name),
		Models:  models,
	})
}

// SetCustomProvider makes every session of this adapter know the account's
// own OpenAI-compatible endpoint. nil clears it.
func (p *PiCLIAdapter) SetCustomProvider(custom *PiCustomProvider) error {
	if err := custom.Validate(); err != nil {
		return err
	}
	if custom == nil {
		p.customProvider = nil
		return nil
	}
	copied := *custom
	copied.Models = append([]string(nil), custom.Models...)
	p.customProvider = &copied
	return nil
}

// mergePiModelsJSON adds the custom provider to a deployment template's
// models.json (template may be nil). The account's entry wins on a name clash.
func mergePiModelsJSON(template []byte, custom *PiCustomProvider) ([]byte, error) {
	doc := map[string]json.RawMessage{}
	if len(template) > 0 {
		if err := json.Unmarshal(template, &doc); err != nil {
			return nil, fmt.Errorf("invalid template models.json: %w", err)
		}
	}
	if custom == nil {
		return template, nil
	}
	providers := map[string]json.RawMessage{}
	if raw, ok := doc["providers"]; ok {
		if err := json.Unmarshal(raw, &providers); err != nil {
			return nil, fmt.Errorf("invalid template providers: %w", err)
		}
	}
	entry, err := custom.providerEntry()
	if err != nil {
		return nil, err
	}
	providers[custom.Name] = entry
	encoded, err := json.Marshal(providers)
	if err != nil {
		return nil, err
	}
	doc["providers"] = encoded
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}
