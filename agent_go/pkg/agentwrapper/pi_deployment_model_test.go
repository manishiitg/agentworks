package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/picli"
)

// A deployment with its own Pi provider has a key for that provider only. A project that saved Pi's built-in default
// (a Google model) failed every turn with "No API key found for google"; it now runs on the deployment's model.
func TestDeploymentPiModelReplacesAModelTheDeploymentCannotRun(t *testing.T) {
	if got, changed := deploymentPiModel("pi-cli", "google/gemini-3.8-flash"); changed || got != "google/gemini-3.8-flash" {
		t.Fatalf("no template: got %q changed=%v, want the saved model unchanged", got, changed)
	}
	dir := t.TempDir()
	models := `{"providers":{"gateway":{"baseUrl":"https://gateway.example.com/v1","api":"openai-completions","apiKey":"$GATEWAY_API_KEY","models":[{"id":"model-a","name":"Model A"}]}}}`
	if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(models), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(picli.EnvPiAgentTemplateDir, dir)
	for _, tc := range []struct {
		provider, model, want string
		changed               bool
	}{
		{"pi-cli", "google/gemini-3.8-flash", "gateway/model-a", true},
		{"pi-cli", "", "gateway/model-a", true},
		{"pi-cli", "gateway/model-a", "gateway/model-a", false},
		{"claude-code", "claude-sonnet-5-5", "claude-sonnet-5-5", false},
	} {
		if got, changed := deploymentPiModel(tc.provider, tc.model); got != tc.want || changed != tc.changed {
			t.Errorf("%s %q: got %q changed=%v, want %q changed=%v", tc.provider, tc.model, got, changed, tc.want, tc.changed)
		}
	}
}
