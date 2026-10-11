package videoproduct

import "time"

const routeStepID = "route"

func videoStudioWorkflowManifest(projectID, title string) map[string]interface{} {
	product := mustVideoStudioManifest()
	return map[string]interface{}{"schema_version": 1, "id": "video-" + projectID, "version": "1.0.0", "label": title, "capabilities": map[string]interface{}{"selected_servers": []string{}, "selected_tools": []string{}, "selected_skills": append([]string(nil), product.Workflows.SelectedSkills...), "selected_secrets": []string{}, "browser_mode": product.Workflows.BrowserMode, "use_code_execution_mode": true, "llm_config": videoWorkflowLLMConfig()}, "execution_defaults": map[string]interface{}{"always_use_same_run": true, "workshop_mode": "run"}, "schedules": []interface{}{}, "created_at": time.Now().UTC().Format(time.RFC3339), "updated_at": time.Now().UTC().Format(time.RFC3339)}
}
func videoAgentLLMConfig() map[string]interface{} {
	return map[string]interface{}{"provider": "claude-code", "model_id": DefaultClaudeModel}
}
func videoWorkflowLLMConfig() map[string]interface{} {
	return map[string]interface{}{"schema_version": 2, "mode": "explicit", "builder_llm": videoAgentLLMConfig(), "pulse_llm": videoAgentLLMConfig(), "tiered_config": map[string]interface{}{"tier_1": videoAgentLLMConfig(), "tier_2": videoAgentLLMConfig(), "tier_3": videoAgentLLMConfig()}}
}
func planForAll(pipelines []*Pipeline) map[string]interface{} {
	steps := make([]map[string]interface{}, 0, 1+len(pipelines)*8)
	routes := make([]map[string]interface{}, 0, len(pipelines))
	for _, p := range pipelines {
		routes = append(routes, map[string]interface{}{"route_id": p.ID, "route_name": p.Name, "condition": p.WhenToUse, "next_step_id": p.Stages[0].ID})
	}
	steps = append(steps, map[string]interface{}{"type": "routing", "id": routeStepID, "title": "Route", "routing_question": "Which pipeline does this brief call for?", "routes": routes, "default_route_id": pipelines[0].ID})
	for _, p := range pipelines {
		for i, stage := range p.Stages {
			deps := []string{}
			for prev := 0; prev < i; prev++ {
				deps = append(deps, p.Stages[prev].Output)
				deps = append(deps, p.Stages[prev].Artifacts...)
			}
			steps = append(steps, stageStep(p, stage, deps, i == len(p.Stages)-1))
		}
	}
	return map[string]interface{}{"steps": steps}
}

// stageStep is one production stage. Always a agent — every stage
// here is conversational and judgment-heavy, which is what the plan-authoring
// guidance reserves that type for.
func stageStep(p *Pipeline, stage PipelineStage, deps []string, last bool) map[string]interface{} {
	required := []map[string]interface{}{{"file_name": stage.Output, "must_exist": true}}
	for _, artifact := range stage.Artifacts {
		required = append(required, map[string]interface{}{"file_name": artifact, "must_exist": true})
	}
	step := map[string]interface{}{
		"type": "agent", "id": stage.ID, "title": stage.Title,
		"description": stage.Description, "context_dependencies": deps,
		"context_output":    stage.Output,
		"items":             []map[string]interface{}{stageExecuteItem()},
		"validation_schema": map[string]interface{}{"files": required},
	}
	if last {
		step["next_step_id"] = "end"
	}
	return step
}

// stageExecuteItem is the one turn a production stage runs. Every stage here is
// conversational and judgment-heavy — writing a brief, a storyboard, a design,
// a critique — which is exactly what the plan-authoring guidance means by
// "use agent for every conversational, judgment-heavy, browser-driven
// or adaptive step, even when it needs only one message. Non-scripted regular
// steps are unsupported." (frontend/src/commands/builtin-commands.tsx).
//
// These stages were authored as `regular` and only ran because the runtime
// rewrites every non-scripted regular step into a sequence exactly like this one
// (normalizeRegularStepToAgent). Declaring it makes the stored plan
// say what actually executes, so the plan UI no longer has to reconstruct it and
// a stage that later wants a second turn — a critique pass, a repair gate — can
// simply add an item instead of needing a different step type.
//
// The wording is the runtime's own synthesized message, kept verbatim so this
// change alters what the plan DECLARES, not how a stage behaves.
func stageExecuteItem() map[string]interface{} {
	return map[string]interface{}{
		"id":   "execute-and-verify",
		"type": "user_message",
		"kind": "execution",
		"message": "Complete the step described above. Re-open the produced evidence, verify the result against every stated requirement, " +
			"repair any gap you find, and finish only when the step is complete.",
	}
}

// managedSkillPolicy splits a stage's declared skills by how the product says
// each one reaches the agent. product.yaml installs the HyperFrames production
// stack but declares `attach: [hyperframes]`; the rest are, in productdeps' own
// words, "ordinary files for progressive disclosure" and cinematic stages
// read only the parts needed by an explicitly planned insert.
//
// One field was doing both jobs. Every stage asked to ATTACH all of them,
// which failed silently before the loader became workspace-aware and would now
// succeed and load specialist skills nobody asked for — the exact thing
// the product prompt says not to do. So attachment gets the skills declared
// attachable, and the remainder get a read path instead.
func managedSkillPolicy() (installed map[string]bool, attachable map[string]bool) {
	installed = map[string]bool{}
	attachable = map[string]bool{}
	for _, source := range mustVideoStudioManifest().Dependencies.Skills {
		for _, name := range source.Install {
			installed[name] = true
		}
		for _, name := range source.Attach {
			attachable[name] = true
		}
	}
	return installed, attachable
}

// splitStageSkills returns the skills a stage should attach and the workspace
// read paths it needs for the ones it must read from disk instead.
func splitStageSkills(stageSkills []string) (attach []string, readPaths []string) {
	installed, attachable := managedSkillPolicy()
	for _, name := range stageSkills {
		if installed[name] && !attachable[name] {
			readPaths = append(readPaths, "skills/"+name)
			continue
		}
		attach = append(attach, name)
	}
	return attach, readPaths
}

func baseStageAgentConfig() map[string]interface{} {
	return map[string]interface{}{"execution_llm": videoAgentLLMConfig(), "use_code_execution_mode": true, "additional_read_paths": []string{"uploads"}, "learnings_access": "none", "knowledgebase_access": "none", "db_access": "none"}
}
func stepConfigForAll(pipelines []*Pipeline) map[string]interface{} {
	steps := make([]map[string]interface{}, 0, len(pipelines)*8)
	for _, p := range pipelines {
		for _, stage := range p.Stages {
			config := baseStageAgentConfig()
			if len(stage.Skills) > 0 {
				attach, diskPaths := splitStageSkills(stage.Skills)
				if len(attach) > 0 {
					config["enabled_skills"] = attach
				}
				if len(diskPaths) > 0 {
					// enabled_skills also drives the folder guard, so skills that
					// move out of it must get their read path back explicitly or
					// the agent cannot open the file it is told to read.
					config["additional_read_paths"] = append(config["additional_read_paths"].([]string), diskPaths...)
				}
			}
			steps = append(steps, map[string]interface{}{"id": stage.ID, "title": stage.Title, "agent_configs": config})
		}
	}
	return map[string]interface{}{"steps": steps}
}
