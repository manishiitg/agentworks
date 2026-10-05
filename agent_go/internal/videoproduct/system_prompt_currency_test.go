package videoproduct

import (
	"strings"
	"testing"
)

// The system prompt is the one place describing the product that no compiler
// checks, so it drifts silently: it still called show_video "the one Video
// Studio presentation tool" after two more were added, still listed two
// workflows after four existed, and still sent users to a "Videos panel"
// that had been renamed. Each of those is invisible until an agent acts on
// it in front of a user. These assertions tie the prompt to the code it
// describes.
func TestSystemPromptMatchesTheProductItDescribes(t *testing.T) {
	manifest, err := VideoStudioManifest()
	if err != nil {
		t.Fatal(err)
	}
	prompt, err := productConfigFiles.ReadFile(manifest.Prompt.File)
	if err != nil {
		t.Fatalf("read system prompt %q: %v", manifest.Prompt.File, err)
	}
	text := string(prompt)

	// Every presentation tool the product admits must be one the prompt tells
	// the agent when to call. A registered tool nobody is told to use is dead
	// weight; the character one especially, since its whole value is being
	// called before generation rather than after.
	for _, tool := range []string{"show_video", "show_character", "show_reference", "show_document"} {
		if !strings.Contains(text, tool) {
			t.Fatalf("the system prompt never mentions %s, so the agent will not call it", tool)
		}
	}

	// The panel name is user-facing: the prompt tells the agent to say where
	// the video is, and naming a panel that no longer exists sends the user
	// looking for a tab that is not there.
	if strings.Contains(text, "Videos panel") {
		t.Fatal("the system prompt still refers to the Videos panel, which is now the Production panel")
	}
	if !strings.Contains(text, "Production panel") {
		t.Fatal("the system prompt no longer tells the agent where finished work appears")
	}

	// Paid generation bills the user per call. HyperFrames can be used for a
	// deterministic insert without becoming a separate product route.
	if !strings.Contains(strings.ToLower(text), "approval of a storyboard is not approval to spend") {
		t.Fatal("the system prompt no longer warns that an approved plan is not approval to spend")
	}
	if !strings.Contains(text, "live H3 rate") || !strings.Contains(text, "bounded H3 estimate") || !strings.Contains(text, "approved retry allowance") {
		t.Fatal("the system prompt no longer requires a bounded, live-rate H3 estimate before paid generation")
	}
	for _, timingContract := range []string{
		"H3 waiting rule",
		"up to 15 minutes",
		"save the request ID immediately",
		"never resubmit, abandon, or call the clip failed merely because a short tool wait elapsed",
	} {
		if !strings.Contains(text, timingContract) {
			t.Fatalf("the system prompt lost the H3 async timing contract: missing %q", timingContract)
		}
	}

	for _, forbidden := range []string{
		"run_full_workflow", "execute_step", "query_step", "send_step_message",
		"stop_step", "stop_all_executions", "list_executions",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("Video Studio must not describe workflow tool %q", forbidden)
		}
	}
	if !strings.Contains(text, "MCP-only in this browser product") ||
		!strings.Contains(text, "terminal-style selectors") ||
		!strings.Contains(text, "exist only through the MCP bridge") {
		t.Fatal("the system prompt must require the bridge-only browser tool surface")
	}
	if !strings.Contains(strings.ToLower(text), "long-form video") || !strings.Contains(text, "longform-cinematic-video") {
		t.Fatal("the system prompt no longer defaults fresh productions to cinematic direction")
	}
	if strings.Contains(text, "The available stage plans are `infographic`") || !strings.Contains(text, "complete animated or motion-graphics sequences") {
		t.Fatal("the system prompt must keep HyperFrames inside cinematic production rather than exposing an infographic product")
	}
	for _, skill := range manifest.Profile.Skills {
		if skill == "product-infographic" {
			t.Fatal("product-infographic remains exposed in the Video Studio profile")
		}
	}
	for _, audioContract := range []string{
		"Use this production order in direct chat",
		"video-look-sound",
		"locations/backgrounds",
		"native synchronized dialogue",
		"explicitly show the user the speech-design tradeoff",
		"visible lip-synced dialogue",
		"hybrid with native dialogue",
		"Never let an audio-incapable endpoint silently decide",
		"Use separate TTS only for off-camera voiceover",
		"measure its real duration",
		"build the shot list around the measured audio",
		"without its promised audio",
		"cinematic-visual-development",
		"Text direction is valid for wardrobe",
	} {
		if !strings.Contains(text, audioContract) {
			t.Fatalf("the system prompt lost the direct-chat look/sound contract: missing %q", audioContract)
		}
	}
	for _, contract := range []string{"direct chat", "relevant attached skills", "Do not expose, offer, or ask the user to choose workflow machinery"} {
		if !strings.Contains(text, contract) {
			t.Fatalf("the system prompt no longer requires skill-led direct chat: missing %q", contract)
		}
	}
	for _, tool := range []string{"read_image", "native web search tool", "hand-drawn illustration", "Instagram/Reel brief"} {
		if !strings.Contains(text, tool) {
			t.Fatalf("the system prompt lost Video Studio's cinematic research contract: missing %q", tool)
		}
	}
}
