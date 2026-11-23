package picli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A deployment's Pi provider (Citymall's gateway, PLAT ticket in agent_go) is staged into every session agent dir, and
// the template can never carry the key itself: it must reference the variable the scoped credential path injects.
func TestPiAgentTemplateStagedIntoSessionDirAndRefusesLiteralKeys(t *testing.T) {
	template := t.TempDir()
	models := `{"providers":{"citymall":{"baseUrl":"https://gw.example/chat/v1/gpt-6-luna","api":"openai-completions",
		"apiKey":"$CITYMALL_API_KEY","headers":{"api-key":"$CITYMALL_API_KEY"},"authHeader":false,
		"models":[{"id":"gpt-6-luna","name":"GPT-6 Luna","reasoning":true,"thinkingLevelMap":{"off":"none"}}]}}}`
	settings := `{"modelThinkingLevels":{"citymall/gpt-6-luna":"off"}}`
	if err := os.WriteFile(filepath.Join(template, "models.json"), []byte(models), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(template, "settings.json"), []byte(settings), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvPiAgentTemplateDir, template)

	agentDir, _, cleanup, err := preparePiNativeMCPConfig(t.TempDir(), "session-1", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cleanup != nil {
		cleanup()
	}
	for name, want := range map[string]string{"models.json": models, "settings.json": settings} {
		got, err := os.ReadFile(filepath.Join(agentDir, name))
		if err != nil || string(got) != want {
			t.Fatalf("%s not staged: %v", name, err)
		}
		if info, _ := os.Stat(filepath.Join(agentDir, name)); info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode %v, want 0600", name, info.Mode().Perm())
		}
	}
	listed := false
	for _, meta := range GetAllPiCLIModels() {
		listed = listed || meta.ModelID == "citymall/gpt-6-luna"
	}
	if !listed {
		t.Fatal("the template's model is not in the Pi model list")
	}
	if env := piAPIKeyEnv("citymall", "k"); len(env) != 1 || env[0] != "CITYMALL_API_KEY=k" {
		t.Fatalf("key env = %v", env)
	}

	for _, bad := range []string{
		strings.Replace(models, `"apiKey":"$CITYMALL_API_KEY"`, `"apiKey":"sk-literal"`, 1),
		strings.Replace(models, `"api-key":"$CITYMALL_API_KEY"`, `"api-key":"literal"`, 1),
		strings.Replace(models, `"apiKey":"$CITYMALL_API_KEY"`, `"apiKey":"!cat /secret"`, 1),
		strings.Replace(models, `"apiKey":"$CITYMALL_API_KEY"`, `"apiKey":"$OTHER_KEY"`, 1),
	} {
		if err := os.WriteFile(filepath.Join(template, "models.json"), []byte(bad), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, _, _, err := preparePiNativeMCPConfig(t.TempDir(), "session-2", nil, nil); err == nil {
			t.Fatalf("template accepted: %s", bad)
		}
	}
}

// A person's own OpenAI-compatible endpoint (an AgentWorks "bring your own key" account) is staged into that
// session's models.json by env reference only: the key itself never lands on disk, and a bad base URL is refused.
func TestPiCustomProviderStagedByKeyReference(t *testing.T) {
	t.Setenv(EnvPiAgentTemplateDir, "")
	custom := &PiCustomProvider{Name: "openai-compatible", BaseURL: "https://llm.example.com/v1/", Models: []string{"qwen3-coder"}}
	agentDir, _, cleanup, err := preparePiNativeMCPConfig(t.TempDir(), "session-c", nil, custom)
	if err != nil {
		t.Fatal(err)
	}
	if cleanup != nil {
		cleanup()
	}
	got, err := os.ReadFile(filepath.Join(agentDir, "models.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"baseUrl": "https://llm.example.com/v1"`, `"apiKey": "$OPENAI_COMPATIBLE_API_KEY"`, `"api": "openai-completions"`, `"id": "qwen3-coder"`} {
		if !strings.Contains(string(got), want) {
			t.Fatalf("models.json lacks %s:\n%s", want, got)
		}
	}
	if env := piAPIKeyEnv("openai-compatible", "k"); len(env) != 1 || env[0] != "OPENAI_COMPATIBLE_API_KEY=k" {
		t.Fatalf("key env = %v", env)
	}
	for _, bad := range []*PiCustomProvider{
		{Name: "openai-compatible", BaseURL: "file:///etc/passwd", Models: []string{"m"}},
		{Name: "openai-compatible", BaseURL: "https://u:p@host/v1", Models: []string{"m"}},
		{Name: "openai-compatible", BaseURL: "https://host/v1", Models: []string{"!cat /secret"}},
		{Name: "Bad Name", BaseURL: "https://host/v1", Models: []string{"m"}},
	} {
		if (&PiCLIAdapter{}).SetCustomProvider(bad) == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
}
