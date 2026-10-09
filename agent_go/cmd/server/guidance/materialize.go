package guidance

import (
	"fmt"
	"log"
	"sort"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// MaterializeReferenceSkill bundles every mode-allowed entry in
// referenceKinds into ONE Anthropic-pattern skill. Every mode uses the stable
// "builder-reference" identity so prompts and agents never need to know which
// transport or execution surface materialized the bundle.
// the SKILL.md body is a table of contents; the deep content lives in
// references/<kind>.md supporting files. The agent's CLI matches the skill
// by description, then reads the specific reference file it needs.
//
// Returns nil if no kinds are allowed in the given mode (so callers can skip
// attaching without an extra check).
func MaterializeReferenceSkill(mode string) *llmtypes.Skill {
	return materializeReferenceSkillWithMCP(mode, true)
}

func materializeReferenceSkillWithMCP(mode string, mcpManagement bool) *llmtypes.Skill {
	spec := referenceSkillSpecForMode(mode)
	if !mcpManagement {
		spec.Intro = "Read the matching reference before acting. This session cannot install or configure MCP integrations; an interactive Builder with write access can do that. Reading documentation does not grant tools."
	}
	return buildMegaSkill(buildMegaSkillSpec{
		Mode:             mode,
		Registry:         referenceKinds,
		Name:             spec.Name,
		DescriptionIntro: spec.DescriptionIntro,
		Intro:            spec.Intro,
		Render:           renderReferenceKind,
		Select: func(kind string, meta kindMeta) bool {
			// Pulse's skills live only in Pulse's own pack (MaterializePulseSkill).
			return kind != "workspace-views" && !isPulseSkillKind(kind) && (mcpManagement || kind != "integration-discovery") && (mode == "" || modeAllowedIn(kind, mode, referenceKinds))
		},
	})
}

func materializeWorkflowUIControlSkill(mode string) (*llmtypes.Skill, error) {
	if !modeAllowedIn("workspace-views", mode, referenceKinds) {
		return nil, nil
	}
	content, err := renderReferenceKind("workspace-views", tmplData{WorkshopMode: mode})
	if err != nil {
		return nil, fmt.Errorf("render workflow UI control skill: %w", err)
	}
	return &llmtypes.Skill{
		Name:        "workflow-ui-control",
		Description: "Present the correct AgentWorks Workflow view with acknowledged UI control. Use for Workflow Builder or Run workspace navigation; do not use Crew panel IDs or semantics.",
		Content:     content,
		Metadata:    map[string]string{"mode": mode, "kind": "workspace-views"},
		Source:      llmtypes.SkillSource{Origin: "builtin"},
	}, nil
}

type referenceSkillSpec struct {
	Name             string
	DescriptionIntro string
	Intro            string
}

// referenceSkillSpecForMode's DescriptionIntro is deliberately just a short
// framing sentence, not an enumeration of topics -- buildMegaSkill appends
// the full, always-current topic list itself (see its doc comment). A
// hand-enumerated version here previously went stale as referenceKinds grew,
// which is exactly how add_mcp_server, browser CDP, and Gmail connection
// scope guidance all silently stopped being discoverable for their matching
// queries.
func referenceSkillSpecForMode(mode string) referenceSkillSpec {
	if mode == "multi-agent" {
		return referenceSkillSpec{
			Name:             "builder-reference",
			DescriptionIntro: "Product chat reference docs — detailed contracts and rules to consult before specific actions.",
			Intro:            "This skill bundles product chat reference documentation. Match it when you need detailed rules, patterns, or contracts for any of the topics below — especially LLM/provider configuration via tools, not by reading or editing `config/` files. Read the single matching file under `references/`. You don't need to read more than one unless the action spans multiple topics.",
		}
	}

	if mode == "run" {
		return referenceSkillSpec{
			Name:             "builder-reference",
			DescriptionIntro: "Workflow runtime references to consult before acting. Run cannot edit workflow design.",
			Intro:            "Read the reference matching this runtime request. The current mode and granted tools define authority; reading a reference does not permit design/config mutations or grant additional tools.",
		}
	}

	return referenceSkillSpec{
		Name:             "builder-reference",
		DescriptionIntro: "Workflow docs.",
		Intro:            "This skill bundles the workflow workshop's reference documentation. Match it when you need detailed rules, patterns, or contracts for any of the topics below — especially LLM/provider configuration via tools, not by reading or editing `config/` files; connecting a new third-party service/tool, using integration-discovery for catalog and internet search, provider verification, and installation through install_mcp_server (or add_mcp_server for known custom config), never hand-editing managed MCP config; browser/CDP automation; and Gmail/Google Workspace connection scope or permission issues. Read the single matching file under `references/`. You don't need to read more than one unless the action spans multiple topics.",
	}
}

// MaterializeGuidanceSkill bundles every mode-allowed entry in allKinds into
// ONE skill named "workflow-commands". Same Anthropic pattern: SKILL.md is
// the TOC, references/<kind>.md is the procedural flow for each slash
// command (design-plan, pulse-review, define-success, strategy-auditor, ...).
//
// Procedural flows benefit from Focus/Iteration context when invoked via
// get_workflow_command_guidance — the materialized version is the no-context
// rendering, intended as a fallback for callers that don't go through that
// tool.
func MaterializeGuidanceSkill(mode string) *llmtypes.Skill {
	return buildMegaSkill(buildMegaSkillSpec{
		Mode:             mode,
		Registry:         allKinds,
		Name:             "workflow-commands",
		DescriptionIntro: "Workflow workshop slash-command flows — canonical procedural guidance for each command below.",
		Intro:            "This skill bundles the workshop's canonical slash-command procedures. Match it when the user invokes one of these commands (e.g. `/design-plan`, `/pulse-review`) or describes the same intent in plain chat. Read the single matching file under `references/` — the prose there is your instructions for the turn, follow it verbatim.",
		Render:           renderKind,
	})
}

// AttachReferenceSurface attaches the consolidated reference surface to the
// agent:
//
//   - system-tools (existing meta-skill: explains the tool surface and
//     read_skill)
//   - builder-reference (mode-filtered mega-skill bundling every
//     reference doc allowed in the current mode; SKILL.md TOC +
//     references/<kind>.md per topic)
//   - workflow-ui-control (the Workflow-only view and receipt contract)
//   - workflow-commands (mega-skill bundling every procedural flow)
//
// Why three folders instead of one per kind: ~25 individual skill folders
// per session bloats every CLI's skill listing (each entry costs prompt
// tokens), confuses description-based matching, and clutters the
// projection adapters' output dirs. Bundling related material under one
// skill with references/ subfiles is exactly the progressive-disclosure
// shape Anthropic's skill spec is designed for.
//
// mcpagent exposes every attached bundle through its intrinsic read_skill tool
// on API and coding-CLI transports. Native CLI projection is an optimization,
// not a separate access contract.
// MaterializeReferenceKindsAsSkills renders each named referenceKinds entry
// as its OWN individually-named skill, for a product that wants to declare
// reference material in profile.skills[] the way it would declare any other
// product-owned skill -- e.g. a product declaring "backup-strategy" or
// "secret-management" by name -- rather than relying on the mode-gated
// "builder-reference" bundle AttachReferenceSurface produces. mode gates
// eligibility exactly as it does there; requesting a kind not allowed in
// mode, or a name that doesn't exist, is an error (a product declaring a
// skill it can't actually have is a configuration bug, not something to
// silently drop).
//
// Reference templates (templates/system/*.md) don't use tmplData's per-turn
// fields (Focus/Iteration/RunFolder/WorkshopMode) -- they're static
// background material, not per-invocation guided flows -- so rendering once
// here with a zero-value tmplData is safe and produces the same content
// AttachReferenceSurface's bundle would have shown under this name.
func MaterializeReferenceKindsAsSkills(mode string, names []string) ([]*llmtypes.Skill, error) {
	out := make([]*llmtypes.Skill, 0, len(names))
	for _, name := range names {
		meta, ok := referenceKinds[name]
		if !ok {
			return nil, fmt.Errorf("unknown reference kind %q", name)
		}
		if !modeAllowedIn(name, mode, referenceKinds) {
			return nil, fmt.Errorf("reference kind %q is not allowed in mode %q", name, mode)
		}
		content, err := renderReferenceKind(name, tmplData{})
		if err != nil {
			return nil, fmt.Errorf("render reference kind %q: %w", name, err)
		}
		out = append(out, &llmtypes.Skill{
			Name:        name,
			Description: meta.Description,
			Content:     content,
			Source:      llmtypes.SkillSource{Origin: "builtin"},
		})
	}
	return out, nil
}

func AttachReferenceSurface(mode string, attach func(*llmtypes.Skill) error) error {
	return AttachReferenceSurfaceWithMCP(mode, true, attach)
}

func AttachReferenceSurfaceWithMCP(mode string, mcpManagement bool, attach func(*llmtypes.Skill) error) error {
	return AttachConfiguredReferenceSurface(mode, mcpManagement, []string{"system-tools", "builder-reference", "workflow-ui-control", "workflow-commands"}, attach)
}

// AttachConfiguredReferenceSurface honors the product's ordered bundle list.
// Mode and MCP admission still filter the contents; a YAML entry grants no tools.
func AttachConfiguredReferenceSurface(mode string, mcpManagement bool, names []string, attach func(*llmtypes.Skill) error) error {
	if attach == nil {
		return fmt.Errorf("attach reference surface: nil attach function")
	}
	seen := map[string]bool{}
	for _, name := range names {
		if seen[name] {
			return fmt.Errorf("duplicate reference skill %q", name)
		}
		seen[name] = true
		switch name {
		case "system-tools", "builder-reference", "workflow-ui-control", "workflow-commands":
		default:
			return fmt.Errorf("unknown reference skill %q", name)
		}
	}
	for _, name := range names {
		var skill *llmtypes.Skill
		switch name {
		case "system-tools":
			skill = buildSystemToolsSkillWithMCP(mode, mcpManagement)
			if skill != nil && !mcpManagement {
				skill.Content += "\nMCP management is unavailable in this session. Do not attempt integration installation or manual host config edits; ask an interactive Builder with write access to configure it.\n"
			}
		case "builder-reference":
			skill = materializeReferenceSkillWithMCP(mode, mcpManagement)
		case "workflow-ui-control":
			var err error
			skill, err = materializeWorkflowUIControlSkill(mode)
			if err != nil {
				return err
			}
		case "workflow-commands":
			skill = MaterializeGuidanceSkill(mode)
		}
		if skill != nil {
			if err := attach(skill); err != nil {
				return fmt.Errorf("attach %s: %w", name, err)
			}
		}
	}
	return nil
}

// buildMegaSkillSpec captures the inputs for buildMegaSkill so the two
// mega-skill constructors don't have to repeat the same plumbing.
type buildMegaSkillSpec struct {
	Mode     string
	Registry map[string]kindMeta
	Name     string
	// DescriptionIntro is a short, hand-written framing sentence. The full
	// outer Description is this sentence plus the rendered topic names.
	// Full topic descriptions belong in the body, keeping discovery metadata
	// within the skill format's 1024-character limit.
	DescriptionIntro string
	Intro            string
	Render           func(kind string, data tmplData) (string, error)

	// Select overrides mode filtering when non-nil. Step execution selects by
	// the tools the agent actually holds rather than by mode — see PLAT-124 and
	// MaterializeStepExecutionReferenceSkill.
	Select func(kind string, meta kindMeta) bool
}

// buildMegaSkill assembles one Anthropic-pattern skill from a kind registry:
// SKILL.md body = Intro + a TOC listing every mode-allowed kind with its
// description and a pointer to references/<kind>.md; SupportingFiles = one
// rendered template per kind. Returns nil if no kinds are allowed (so the
// caller can skip attachment).
func buildMegaSkill(spec buildMegaSkillSpec) *llmtypes.Skill {
	kinds := kindEnumFrom(spec.Registry)
	sort.Strings(kinds)

	allowed := make([]string, 0, len(kinds))
	for _, k := range kinds {
		if spec.Select != nil {
			if spec.Select(k, spec.Registry[k]) {
				allowed = append(allowed, k)
			}
			continue
		}
		if spec.Mode == "" || modeAllowedIn(k, spec.Mode, spec.Registry) {
			allowed = append(allowed, k)
		}
	}
	if len(allowed) == 0 {
		return nil
	}

	files := make([]llmtypes.SkillFile, 0, len(allowed))
	topics := make([]string, 0, len(allowed))
	var toc strings.Builder
	for _, k := range allowed {
		meta := spec.Registry[k]
		text, err := spec.Render(k, tmplData{WorkshopMode: spec.Mode})
		if err != nil {
			// A render failure is a programming error in an embedded
			// template, but crashing the session over one bad kind is
			// worse than attaching the skill without it — the kind is
			// will be absent from read_skill's file list, making the
			// programming error visible without crashing the session.
			log.Printf("[GUIDANCE] materialize %s/%s: %v (skipping this reference)", spec.Name, k, err)
			continue
		}
		files = append(files, llmtypes.SkillFile{
			RelPath: "references/" + k + ".md",
			Content: []byte(text),
		})
		topics = append(topics, k)
		fmt.Fprintf(&toc, "- `references/%s.md` — %s\n", k, meta.Description)
	}

	body := spec.Intro + "\n\n## Available references\n\n" + toc.String()

	// Generate the compact discovery list from the same references as the
	// detailed TOC so new topics remain discoverable without duplicating the
	// entire reference catalog into every agent's initial context.
	// Topics join without spaces: with 60+ topics the separators alone cost a
	// tenth of the skill format's 1024-character description budget, and every
	// new topic must stay discoverable without stealing that budget. Topics
	// that share a first word are written once with braces
	// (pulse-{gate,finalizer}); the full names stay in the SKILL.md index.
	description := spec.DescriptionIntro + " Topics: " + compactTopicList(topics) + "."

	return &llmtypes.Skill{
		Name:            spec.Name,
		Description:     description,
		Content:         body,
		SupportingFiles: files,
		Metadata: map[string]string{
			"mode":  spec.Mode,
			"kinds": strings.Join(allowed, ","),
		},
		Source: llmtypes.SkillSource{Origin: "builtin"},
	}
}

// compactTopicList joins sorted topic names for a skill description, writing
// topics that share a first word once: pulse-{gate,finalizer}, goal-lead-{check,work}. Every name
// still reads in full when its braces are expanded, and the description stays
// within the 1024-character limit as topics are added.
func compactTopicList(topics []string) string {
	type group struct {
		prefix string
		rests  []string
	}
	groups := []*group{}
	byPrefix := map[string]*group{}
	for _, topic := range topics {
		prefix, rest, found := strings.Cut(topic, "-")
		if !found {
			groups = append(groups, &group{rests: []string{topic}})
			continue
		}
		if g, ok := byPrefix[prefix]; ok {
			g.rests = append(g.rests, rest)
			continue
		}
		g := &group{prefix: prefix, rests: []string{rest}}
		byPrefix[prefix] = g
		groups = append(groups, g)
	}
	parts := make([]string, 0, len(groups))
	for _, g := range groups {
		switch {
		case g.prefix == "":
			parts = append(parts, g.rests[0])
		case len(g.rests) == 1:
			parts = append(parts, g.prefix+"-"+g.rests[0])
		default:
			// Factor out every further word the group shares:
			// goal-lead-{check,work}.
			prefix, rests := g.prefix+"-", g.rests
			for {
				word, _, found := strings.Cut(rests[0], "-")
				shared := found
				for _, rest := range rests {
					if !strings.HasPrefix(rest, word+"-") || rest == word+"-" {
						shared = false
					}
				}
				if !shared {
					break
				}
				prefix += word + "-"
				trimmed := make([]string, len(rests))
				for i, rest := range rests {
					trimmed[i] = strings.TrimPrefix(rest, word+"-")
				}
				rests = trimmed
			}
			parts = append(parts, prefix+"{"+strings.Join(rests, ",")+"}")
		}
	}
	return strings.Join(parts, ",")
}
