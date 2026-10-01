package agentworksproduct

import (
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

func TestChatDefinitions(t *testing.T) {
	m := mustAgentWorksManifest()
	for _, mode := range []string{"builder", "run"} {
		if ChatPromptTemplate(mode) == "" || ChatDefinitionKey(mode) == "" {
			t.Fatal("empty chat definition")
		}
		skills := ChatSkills(mode)
		skills[0] = "changed"
		if ChatSkills(mode)[0] == "changed" {
			t.Fatal("caller changed configured skills")
		}
		tools := ChatTools(mode)
		tools[0] = "changed"
		if ChatTools(mode)[0] == "changed" {
			t.Fatal("caller changed configured tools")
		}
	}
	if ChatAllowsTool("builder", "human_feedback") || !ChatAllowsTool("run", "human_feedback") {
		t.Fatal("human_feedback must be run-only")
	}
	if !ChatAllowsTool("builder", "answer_human_input_request") || ChatAllowsTool("run", "answer_human_input_request") {
		t.Fatal("answer_human_input_request must be builder-only")
	}
	for _, mode := range []string{"builder", "run"} {
		for _, name := range []string{"notify_user", "get_notification_history"} {
			if !ChatAllowsTool(mode, name) {
				t.Fatalf("%s chat is missing %s", mode, name)
			}
		}
	}
	if ChatDefinitionKey("builder") == ChatDefinitionKey("run") {
		t.Fatal("mode definitions must differ")
	}
	if !containsChatSkill(ChatSkills("builder"), "ui-ux-pro-max") || containsChatSkill(ChatSkills("run"), "ui-ux-pro-max") {
		t.Fatal("UI/UX Pro Max must be available to Builder only")
	}
	for _, mode := range []string{"builder", "run"} {
		if !containsChatSkill(ChatSkills(mode), "workflow-ui-control") {
			t.Fatalf("%s chat is missing workflow-ui-control", mode)
		}
	}
	for _, mode := range []string{"builder", "run"} {
		prompt := ChatPromptTemplate(mode)
		for _, want := range []string{
			"Producing schedules also default to sequential",
			"Separate iteration folders do not isolate shared state",
			"parallel schedules require explicit human approval",
		} {
			if !strings.Contains(prompt, want) {
				t.Errorf("%s prompt missing schedule parallel-risk contract %q", mode, want)
			}
		}
	}
	for _, names := range [][]string{{"missing"}, {"system-tools", "system-tools"}, {}} {
		copy := m
		copy.Chat = map[string]agentprofiles.ChatModeDefinition{"builder": m.Chat["builder"], "run": m.Chat["run"]}
		def := copy.Chat["builder"]
		def.Skills = names
		copy.Chat["builder"] = def
		if validateChatDefinitions(productConfigFiles, copy) == nil {
			t.Fatalf("accepted skills %v", names)
		}
	}
}

func TestRunExternalToolsAdmission(t *testing.T) {
	m := mustAgentWorksManifest()
	admitted := RunExternalTools()
	if len(admitted) == 0 {
		t.Fatal("run mode admits no external tools")
	}
	admitted[0] = "changed"
	if RunExternalTools()[0] == "changed" {
		t.Fatal("caller changed admitted external tools")
	}
	badBuilder := m
	badBuilder.Chat = map[string]agentprofiles.ChatModeDefinition{"builder": m.Chat["builder"], "run": m.Chat["run"]}
	def := badBuilder.Chat["builder"]
	def.ExternalTools = []string{"list_workflows"}
	badBuilder.Chat["builder"] = def
	if validateChatDefinitions(productConfigFiles, badBuilder) == nil {
		t.Fatal("builder mode admitted external tools")
	}
	emptyRun := m
	emptyRun.Chat = map[string]agentprofiles.ChatModeDefinition{"builder": m.Chat["builder"], "run": m.Chat["run"]}
	run := emptyRun.Chat["run"]
	run.ExternalTools = nil
	emptyRun.Chat["run"] = run
	if validateChatDefinitions(productConfigFiles, emptyRun) == nil {
		t.Fatal("run mode admitted no external tools")
	}
}

func containsChatSkill(skills []string, want string) bool {
	for _, skill := range skills {
		if skill == want {
			return true
		}
	}
	return false
}
