package videoproduct

import (
	"encoding/json"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"strings"
	"testing"
)

func TestProductCommandsCarryTheirPromptsAndHideTheirPaths(t *testing.T) {
	manifest, err := VideoStudioManifest()
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Profile.Commands) == 0 {
		t.Fatal("the product declares no slash commands")
	}

	seen := map[string]bool{}
	for _, command := range manifest.Profile.Commands {
		if seen[command.Name] {
			t.Fatalf("duplicate command name %q; the menu would show two identical entries", command.Name)
		}
		seen[command.Name] = true
		if strings.TrimSpace(command.Description) == "" {
			t.Fatalf("command %q has no description, so the menu cannot say what it does", command.Name)
		}
		if strings.TrimSpace(command.Prompt) == "" {
			t.Fatalf("command %q resolved to an empty prompt; it would submit nothing", command.Name)
		}
	}

	// The command that starts a production is the one that has to hold the
	// checkpoints, since a user reaching for it is asking to begin spending.
	var production string
	for _, command := range manifest.Profile.Commands {
		if command.Name == "production" {
			production = command.Prompt
		}
	}
	if production == "" {
		t.Fatal("no /production command; the interview has no entry point")
	}
	for _, want := range []string{"ceiling", "recur", "wait"} {
		if !strings.Contains(strings.ToLower(production), want) {
			t.Fatalf("/production no longer asks about %q before spending", want)
		}
	}

	// /characters is a full character-management entry point -- design a new
	// one, review what exists, or revise one already in use -- not only a
	// lookup. Each path is a real capability the command has to actually
	// cover, not just mention.
	var characters string
	for _, command := range manifest.Profile.Commands {
		if command.Name == "characters" {
			characters = command.Prompt
		}
	}
	if characters == "" {
		t.Fatal("no /characters command")
	}
	for _, want := range []string{"design", "already exist", "asking to change"} {
		if !strings.Contains(strings.ToLower(characters), want) {
			t.Fatalf("/characters no longer covers %q as one of its paths", want)
		}
	}

	// File is the product's own layout detail. Serialising it would ship an
	// internal path to every browser that loads the profile.
	encoded, err := json.Marshal(manifest.Profile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "commands/production.md") {
		t.Fatal("the command's source file path is serialized to the client")
	}
	if !strings.Contains(string(encoded), `"prompt"`) {
		t.Fatal("commands serialize without their prompt, so the client has nothing to submit")
	}
}

// Before this, a product had no way to say which credentials its skills
// need, so nothing could answer "what does this workspace still need" before
// a paid run started -- a user found that out only when a generation call
// failed partway through. This pins that both credentials this product
// actually reads (see fal-ai and google-ai's Authentication sections) are
// declared, and that they are grouped rather than each marked independently
// required -- either alone is enough to carry a production, so requiring
// both would be a false constraint no config would ever satisfy exactly.
func TestProductDeclaresTheSecretsItsSkillsRead(t *testing.T) {
	manifest, err := VideoStudioManifest()
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]agentprofiles.SecretBinding{}
	for _, secret := range manifest.Profile.Secrets {
		byName[secret.Name] = secret
	}
	for _, name := range []string{"FAL_KEY", "GEMINI_API_KEY"} {
		secret, ok := byName[name]
		if !ok {
			t.Fatalf("product.yaml does not declare %s, but fal-ai/google-ai read it from the environment", name)
		}
		if strings.TrimSpace(secret.Description) == "" {
			t.Fatalf("%s has no description; a user deciding whether to configure it has nothing to read", name)
		}
	}
	if byName["FAL_KEY"].Group == "" || byName["FAL_KEY"].Group != byName["GEMINI_API_KEY"].Group {
		t.Fatal("FAL_KEY and GEMINI_API_KEY must share a group -- either alone carries a production, so neither should be independently required")
	}
}

// The tools are useless unless the product both declares a kind for them and
// admits them through its allowlist -- the two halves live in different parts
// of product.yaml and each is silently inert without the other.
func TestProductDeclaresAndAdmitsThePresentationTools(t *testing.T) {
	manifest, err := VideoStudioManifest()
	if err != nil {
		t.Fatal(err)
	}

	kinds := map[string]string{}
	for _, tool := range manifest.Profile.Tools {
		if tool.Presentation == nil {
			continue
		}
		kinds[tool.ID] = tool.Presentation.Kind
	}
	for id, want := range map[string]string{
		"media.show-video":     "media.video",
		"media.show-character": "media.character",
		"media.show-reference": "media.reference",
		"media.show-document":  "document.markdown",
	} {
		if kinds[id] != want {
			t.Fatalf("%s declares presentation kind %q, want %q", id, kinds[id], want)
		}
	}

	admitted := map[string]bool{}
	for _, name := range manifest.Profile.ToolPolicy.Enabled {
		admitted[name] = true
	}
	for _, name := range []string{"show_video", "show_character", "show_reference", "show_document"} {
		if !admitted[name] {
			t.Fatalf("%s is not in the tool allowlist, so the agent never receives it: %v", name, manifest.Profile.ToolPolicy.Enabled)
		}
	}
}
