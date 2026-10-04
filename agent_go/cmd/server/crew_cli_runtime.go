package server

import (
	"fmt"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/workproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

func crewCLIMode(readOnly bool) string {
	if readOnly {
		return "run"
	}
	return "builder"
}

func crewCLIWorkingDir(folder, user, session, provider string, readOnly bool) (string, error) {
	return linkedProjectCLIWorkingDir(folder, user, session, provider, crewCLIMode(readOnly))
}

func crewCLIWorkspaceInstructions(folder string) string {
	return fmt.Sprintf("\nCrew CLI runtime: your current directory holds this session's mode-specific instructions, skills and CLI configuration. `project/` links to the real Crew project at %q. Read project files through `project/<path>`; in Builder, create and edit durable files there too. Use `cd project && ...` for commands that require the project as their working directory; do not put project outputs in the runtime root. Search and glob tools do not look inside the link unless you name it: always pass `project` (or `project/<folder>`) as the search path, because a search from the current directory finds none of the project's files. Workspace bridge paths remain relative to the real Crew project and must not include `project/`. Keep generated CLI instructions/configuration in the runtime root, preserve the project's own instructions, and obey this session's Run/Builder permissions even when reading project guidance.\n", codingAgentWorkspaceWorkingDir(folder))
}

// applyCrewChatMode runs after target admission, before fingerprinting or skill
// loading. Access and the downgrade-only pin choose the prompt and skill set;
// client workshop_mode cannot promote a reader or guest.
func applyCrewChatMode(profile *resolvedAgentProfile, req *QueryRequest, readOnly bool) error {
	if profile == nil || profile.Definition.ID != crewProfileID {
		return nil
	}
	definition := profile.Definition
	oldSkills := definition.Skills
	manifest, err := workproduct.WorkManifest()
	if err != nil {
		return err
	}
	modeSkills := manifest.Chat[crewCLIMode(readOnly)].Skills
	selected := make([]string, 0, len(req.SelectedSkills))
	for _, name := range req.SelectedSkills {
		if name != "crew-run" && name != "crew-builder" {
			selected = append(selected, name)
		}
	}
	req.SelectedSkills = selected
	if readOnly {
		definition.SystemPromptTemplate = workproduct.RunPromptTemplate()
		definition.Runtime.AgentTools.Mode = "mcp_only"
		// Keep project/domain skills, but replace the platform's authoring
		// bundle with the reader contract. These skills guide, never authorize.
		removed := map[string]bool{"crew-builder": true, "crew-run": true}
		for _, name := range oldSkills {
			removed[name] = true
		}
		selected = make([]string, 0, len(req.SelectedSkills))
		for _, name := range req.SelectedSkills {
			if !removed[name] {
				selected = append(selected, name)
			}
		}
		req.SelectedSkills = selected
		definition.Skills = append([]string(nil), modeSkills...)
	} else {
		definition.Skills = appendUniqueStrings(append([]string(nil), oldSkills...), modeSkills...)
	}
	req.SelectedSkills = appendUniqueStrings(req.SelectedSkills, modeSkills...)
	rendered, err := agentprofiles.RenderPrompt(definition, req.AgentProfileContext)
	if err != nil {
		return fmt.Errorf("render Crew %s prompt: %w", crewCLIMode(readOnly), err)
	}
	profile.Definition = definition
	profile.Prompt = strings.TrimSpace(rendered)
	return nil
}
