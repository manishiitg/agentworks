package videoproduct

import (
	"embed"
	"errors"
	"path/filepath"
	"strings"
	"sync"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

const DefaultClaudeModel = "claude-sonnet-5-5"

var videoExtensions = map[string]bool{".mp4": true, ".mov": true, ".webm": true, ".m4v": true}

type qualityReportCheck struct {
	Status   string   `json:"status"`
	Evidence []string `json:"evidence"`
}

type qualityReportFrame struct {
	TimestampSeconds float64 `json:"timestamp_seconds"`
	Path             string  `json:"path"`
}

type qualityReport struct {
	SchemaVersion     int                           `json:"schema_version"`
	CandidatePath     string                        `json:"candidate_path"`
	ContactSheetPath  string                        `json:"contact_sheet_path"`
	Verdict           string                        `json:"verdict"`
	ReadyToPresent    bool                          `json:"ready_to_present"`
	Checks            map[string]qualityReportCheck `json:"checks"`
	SampledFrames     []qualityReportFrame          `json:"sampled_frames"`
	RecommendedAction string                        `json:"recommended_action"`
}

//go:embed skills/*/SKILL.md skills/*/references/*.md skills/*/agents/*.yaml
var profileSkillFiles embed.FS

var profileSkills = []struct{ name, description, path string }{
	{"video-creation", "Direct a conversational video project from brief through reproducible production.", "skills/video-creation/SKILL.md"},
	{"video-look-sound", "Lock a production's visual world, voice, music, ambience, sound-effects, and caption direction before narration or footage is generated.", "skills/video-look-sound/SKILL.md"},
	{"cinematic-visual-development", "Create user-approved subject, location, wardrobe, prop, lighting, and sequence-boundary reference evidence before cinematic footage generation.", "skills/cinematic-visual-development/SKILL.md"},
	{"longform-cinematic-video", "Direct one coherent long-form cinematic film through sequence architecture, continuity-controlled generation, editorial stitching, sound, and seam-by-seam review.", "skills/longform-cinematic-video/SKILL.md"},
	{"multi-clip-cinematic-generation", "Design and generate a coherent sequence of AI video clips with a reference manifest, camera-transition grammar, and durable continuity state.", "skills/multi-clip-cinematic-generation/SKILL.md"},
	{"video-stitching", "Plan, execute, and verify the seam-by-seam cinematic stitching of approved clips into one coherent film.", "skills/video-stitching/SKILL.md"},
	{"video-editing", "Assemble clips, captions, overlays, narration, music, and versioned exports.", "skills/video-editing/SKILL.md"},
	{"video-quality", "Validate candidate videos technically, visually, and editorially.", "skills/video-quality/SKILL.md"},
	{"hyperframes-quality", "Gate editable HyperFrames compositions and rendered evidence for layout, timing, contrast, and motion quality.", "skills/hyperframes-quality/SKILL.md"},
	{"html-composition", "Design deterministic cinematic inserts as HTML/CSS and render them with headless Chrome and ffmpeg.", "skills/html-composition/SKILL.md"},
	{"fal-ai", "Generate AI video, image, voice, and music clips via fal.ai's Node.js client for cinematic productions.", "skills/fal-ai/SKILL.md"},
	{"google-ai", "Generate optional still images and off-camera narration (TTS) through Google's Gemini API. Video Studio uses MiniMax H3 through fal.ai for all footage.", "skills/google-ai/SKILL.md"},
	{"seeddance-api", "Generate supported Seedance video through the direct Seeddance API with durable task recovery and credential-safe server calls.", "skills/seeddance-api/SKILL.md"},
	{"video-provider-capabilities", "Resolve a selected video endpoint's current official schema into a durable request, continuity, cost, retry, and review plan before any paid generation.", "skills/video-provider-capabilities/SKILL.md"},
	{"kling-video", "Use current Kling endpoint controls effectively for text, frames, references, elements, structured multi-shot video, continuity, and native audio.", "skills/kling-video/SKILL.md"},
	{"seedance-video", "Use current Seedance 2.0 and 2.5 endpoint controls effectively for text, frames, multimodal references, editing, continuity, and synchronized audio.", "skills/seedance-video/SKILL.md"},
	{"veo-video", "Use current Veo modes and constraints effectively for text, frames, references, extension, continuity, resolution, and native audio.", "skills/veo-video/SKILL.md"},
	{"minimax-h3-video", "Use Video Studio's MiniMax H3 Reference-to-Video default for multimodal references, continuity, and native stereo audio.", "skills/minimax-h3-video/SKILL.md"},
	{"gemini-omni-video", "Use current fal-hosted Gemini Omni Flash endpoints effectively for generation, multimodal references, iterative localized editing, continuity, and native audio.", "skills/gemini-omni-video/SKILL.md"},
	{"video-model-selection", "Choose which fal.ai or Google model fits one shot's requirements -- input mode, duration, character consistency, native audio, cost -- before generating.", "skills/video-model-selection/SKILL.md"},
	{"video-cinematography", "Construct the generation prompt for one shot -- the five-aspect formula, camera-movement and lighting vocabulary -- and keep a character or subject consistent across shots.", "skills/video-cinematography/SKILL.md"},
	{"video-storytelling", "Structure a video's narrative arc and pacing, scaled from a short explainer to a true long-form (8+ minute) piece's chapters, retention curve, and pattern interrupts.", "skills/video-storytelling/SKILL.md"},
	{"generated-video-quality", "Check AI-generated footage for identity drift, generation artifacts, temporal discontinuity, motion plausibility, lip-sync, color consistency, prompt adherence, and narration alignment. Used alongside video-quality, never in place of it.", "skills/generated-video-quality/SKILL.md"},
}

var registerProductSkillsOnce sync.Once
var registerProductSkillsErr error

func RegisterProductSkills() error {
	registerProductSkillsOnce.Do(func() {
		bindings := make([]agentprofiles.SkillFileBinding, len(profileSkills))
		for i, definition := range profileSkills {
			bindings[i] = agentprofiles.SkillFileBinding{Name: definition.name, Description: definition.description, Path: definition.path}
		}
		registerProductSkillsErr = agentprofiles.RegisterEmbeddedSkills(profileSkillFiles, bindings)
	})
	return registerProductSkillsErr
}

func BuiltinAgentProfile() agentprofiles.Profile {
	manifest := mustVideoStudioManifest()
	profile := manifest.Profile
	profile.SystemPromptTemplate = renderProductPrompt("{{.ProjectTitle}}", "{{.LocalDateTime}}")
	return profile
}

func BuiltinAgentProfiles() []agentprofiles.Profile {
	current := BuiltinAgentProfile()
	legacy := current
	legacy.Version, legacy.Runtime.Provider, legacy.Runtime.ModelID = 1, "", ""
	return []agentprofiles.Profile{legacy, current}
}

func cleanProjectPath(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("path is missing")
	}
	clean := filepath.Clean(filepath.FromSlash(strings.TrimPrefix(raw, "/")))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errors.New("path must stay inside the project")
	}
	return filepath.ToSlash(clean), nil
}
