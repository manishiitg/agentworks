package picli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// EnvPiAgentTemplateDir names a deployment-supplied folder whose Pi
// models.json (custom providers and models) and settings.json (thinking
// defaults only) are staged into every session's private Pi agent dir
// (PI_CODING_AGENT_DIR). Without this, a session-scoped Pi only knows its
// built-in providers, so a deployment cannot add its own gateway.
//
// The files are configuration, never credentials: every apiKey must be an
// environment reference to the key variable this adapter itself injects for
// that provider (<PROVIDER>_API_KEY, see piAPIKeyEnv), so the key arrives
// only through the scoped provider-credential path. "!command" values are
// refused anywhere (Pi would run them on every request).
const EnvPiAgentTemplateDir = "PI_CLI_AGENT_TEMPLATE_DIR"

// piTemplateSettingsKeys are the only settings.json keys a deployment may
// stage: the adapter's own launch flags own everything else (extensions,
// skills, trust, tools).
var piTemplateSettingsKeys = map[string]bool{
	"defaultThinkingLevel": true,
	"modelThinkingLevels":  true,
	"thinkingBudgets":      true,
	"hideThinkingBlock":    true,
}

var piEnvReference = regexp.MustCompile(`^\$(\{[A-Z_][A-Z0-9_]*\}|[A-Z_][A-Z0-9_]*)$`)

// PiTemplateModel is one model a deployment's Pi template declares.
type PiTemplateModel struct {
	Provider      string
	ID            string
	Name          string
	ContextWindow int
	// KeyEnv is the variable the provider's key arrives in (<PROVIDER>_API_KEY).
	KeyEnv string
}

// ModelID is the provider/model selector the app and Pi use.
func (m PiTemplateModel) ModelID() string { return m.Provider + "/" + m.ID }

// PiAgentTemplate is a validated deployment template.
type PiAgentTemplate struct {
	Dir          string
	ModelsJSON   []byte
	SettingsJSON []byte
	Models       []PiTemplateModel
}

// LoadPiAgentTemplate reads and validates the folder named by
// PI_CLI_AGENT_TEMPLATE_DIR. It returns nil, nil when none is configured.
func LoadPiAgentTemplate() (*PiAgentTemplate, error) {
	dir := strings.TrimSpace(os.Getenv(EnvPiAgentTemplateDir))
	if dir == "" {
		return nil, nil
	}
	return loadPiAgentTemplateDir(dir)
}

func loadPiAgentTemplateDir(dir string) (*PiAgentTemplate, error) {
	tmpl := &PiAgentTemplate{Dir: dir}
	models, err := os.ReadFile(filepath.Join(dir, "models.json"))
	if err != nil {
		return nil, fmt.Errorf("%s: cannot read models.json: %w", EnvPiAgentTemplateDir, err)
	}
	if tmpl.Models, err = validatePiTemplateModels(models); err != nil {
		return nil, fmt.Errorf("%s: models.json: %w", EnvPiAgentTemplateDir, err)
	}
	tmpl.ModelsJSON = models
	settings, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	switch {
	case os.IsNotExist(err):
	case err != nil:
		return nil, fmt.Errorf("%s: cannot read settings.json: %w", EnvPiAgentTemplateDir, err)
	default:
		if err := validatePiTemplateSettings(settings); err != nil {
			return nil, fmt.Errorf("%s: settings.json: %w", EnvPiAgentTemplateDir, err)
		}
		tmpl.SettingsJSON = settings
	}
	return tmpl, nil
}

// PiProviderKeyEnv is the one variable the adapter puts a provider's key in.
func PiProviderKeyEnv(provider string) string {
	entries := piAPIKeyEnv(provider, "x")
	if len(entries) == 0 {
		return ""
	}
	name, _, _ := strings.Cut(entries[len(entries)-1], "=")
	return name
}

func validatePiTemplateModels(raw []byte) ([]PiTemplateModel, error) {
	var doc struct {
		Providers map[string]struct {
			APIKey  *string           `json:"apiKey"`
			Headers map[string]string `json:"headers"`
			Models  []struct {
				ID            string            `json:"id"`
				Name          string            `json:"name"`
				ContextWindow int               `json:"contextWindow"`
				Headers       map[string]string `json:"headers"`
			} `json:"models"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	if len(doc.Providers) == 0 {
		return nil, fmt.Errorf("no providers")
	}
	var generic interface{}
	_ = json.Unmarshal(raw, &generic)
	if path := piTemplateCommandValue(generic, "$"); path != "" {
		return nil, fmt.Errorf("%s runs a command (\"!...\"); not allowed", path)
	}
	names := make([]string, 0, len(doc.Providers))
	for name := range doc.Providers {
		names = append(names, name)
	}
	sort.Strings(names)
	var models []PiTemplateModel
	for _, name := range names {
		provider := doc.Providers[name]
		if normalizePiProviderName(name) != name {
			return nil, fmt.Errorf("provider %q must be lower-case letters, digits and dashes", name)
		}
		keyEnv := PiProviderKeyEnv(name)
		if provider.APIKey != nil {
			if err := checkPiTemplateKeyRef(*provider.APIKey, keyEnv); err != nil {
				return nil, fmt.Errorf("provider %s apiKey: %w", name, err)
			}
		}
		if err := checkPiTemplateHeaders(provider.Headers, keyEnv); err != nil {
			return nil, fmt.Errorf("provider %s: %w", name, err)
		}
		for _, model := range provider.Models {
			if strings.TrimSpace(model.ID) == "" {
				return nil, fmt.Errorf("provider %s has a model without id", name)
			}
			if err := checkPiTemplateHeaders(model.Headers, keyEnv); err != nil {
				return nil, fmt.Errorf("model %s/%s: %w", name, model.ID, err)
			}
			models = append(models, PiTemplateModel{Provider: name, ID: model.ID, Name: model.Name, ContextWindow: model.ContextWindow, KeyEnv: keyEnv})
		}
	}
	return models, nil
}

var piProviderNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

func normalizePiProviderName(name string) string {
	if !piProviderNamePattern.MatchString(name) {
		return ""
	}
	return name
}

// checkPiTemplateKeyRef: the key must be a reference to the variable the
// adapter injects, so it can only come from the scoped credential path.
func checkPiTemplateKeyRef(value, keyEnv string) error {
	value = strings.TrimSpace(value)
	if !piEnvReference.MatchString(value) {
		return fmt.Errorf("must be $%s (an environment reference), not a literal key", keyEnv)
	}
	if strings.Trim(value, "${}") != keyEnv {
		return fmt.Errorf("must reference $%s, the variable the provider key is passed in", keyEnv)
	}
	return nil
}

// Header values may be literals (for example a version header) unless the
// header carries a credential, which must be the provider key reference.
func checkPiTemplateHeaders(headers map[string]string, keyEnv string) error {
	for name, value := range headers {
		lower := strings.ToLower(name)
		credential := strings.Contains(lower, "key") || strings.Contains(lower, "auth") || strings.Contains(lower, "token") || strings.Contains(lower, "secret")
		if credential || piEnvReference.MatchString(strings.TrimSpace(value)) {
			if err := checkPiTemplateKeyRef(value, keyEnv); err != nil {
				return fmt.Errorf("header %s: %w", name, err)
			}
		}
	}
	return nil
}

func piTemplateCommandValue(node interface{}, path string) string {
	switch value := node.(type) {
	case map[string]interface{}:
		for key, child := range value {
			if found := piTemplateCommandValue(child, path+"."+key); found != "" {
				return found
			}
		}
	case []interface{}:
		for i, child := range value {
			if found := piTemplateCommandValue(child, fmt.Sprintf("%s[%d]", path, i)); found != "" {
				return found
			}
		}
	case string:
		if strings.HasPrefix(strings.TrimSpace(value), "!") {
			return path
		}
	}
	return ""
}

func validatePiTemplateSettings(raw []byte) error {
	var settings map[string]json.RawMessage
	if err := json.Unmarshal(raw, &settings); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	for key := range settings {
		if !piTemplateSettingsKeys[key] {
			return fmt.Errorf("key %q is not allowed (only thinking settings may be staged)", key)
		}
	}
	return nil
}

// stagePiAgentTemplate writes the deployment template into a session's
// private agent dir. A broken template fails the launch rather than starting
// Pi without the deployment's providers.
//
// custom is the account's own OpenAI-compatible endpoint (PiCustomProvider),
// merged into the template's models.json; nil when the account has none.
func stagePiAgentTemplate(agentDir string, custom *PiCustomProvider) error {
	tmpl, err := LoadPiAgentTemplate()
	if err != nil {
		return err
	}
	if tmpl == nil && custom == nil {
		return nil
	}
	var templateModels []byte
	if tmpl != nil {
		templateModels = tmpl.ModelsJSON
	}
	models, err := mergePiModelsJSON(templateModels, custom)
	if err != nil {
		return err
	}
	if err := writePiPrivateFileAtomically(filepath.Join(agentDir, "models.json"), models); err != nil {
		return fmt.Errorf("failed to stage Pi models.json: %w", err)
	}
	if tmpl != nil && tmpl.SettingsJSON != nil {
		if err := writePiPrivateFileAtomically(filepath.Join(agentDir, "settings.json"), tmpl.SettingsJSON); err != nil {
			return fmt.Errorf("failed to stage Pi settings.json: %w", err)
		}
	}
	return nil
}

// PinnedThinkingLevel is the thinking level a deployment's own settings.json fixes for a model (modelThinkingLevels, keyed
// "provider/model"), when it fixes one. A gateway that rejects tool calls together with reasoning pins its model to "off";
// a person's chosen level (the project default is "high") must not override that, or every turn fails with the gateway's 400.
func (t *PiAgentTemplate) PinnedThinkingLevel(provider, model string) (string, bool) {
	if t == nil || len(t.SettingsJSON) == 0 {
		return "", false
	}
	var settings struct {
		ModelThinkingLevels map[string]string `json:"modelThinkingLevels"`
	}
	if json.Unmarshal(t.SettingsJSON, &settings) != nil {
		return "", false
	}
	for _, key := range []string{provider + "/" + model, model} {
		if level := strings.ToLower(strings.TrimSpace(settings.ModelThinkingLevels[key])); level != "" {
			return level, true
		}
	}
	return "", false
}
