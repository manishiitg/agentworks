package server

import (
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/skills"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/agentworksproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/relayproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/workproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productpolicy"
)

func TestAgentProfileSkillDefaultsReportModesWithoutChangingSelections(t *testing.T) {
	work := workproduct.BuiltinAgentProfile()
	work.BuiltIn = true
	// Arbitrary future feature defaults must appear without a UI name list.
	work.Skills = append(work.Skills, "future-feature-guidance")
	before := append([]string(nil), work.Skills...)
	got, err := agentProfileSkillDefaults(work)
	if err != nil {
		t.Fatal(err)
	}
	manifest, _ := workproduct.WorkManifest()
	if !reflect.DeepEqual(got["run"], manifest.Chat["run"].Skills) {
		t.Fatalf("Run incorrectly inherited Builder defaults: %v", got)
	}
	if !reflect.DeepEqual(got["builder"], appendUniqueStrings(before, manifest.Chat["builder"].Skills...)) {
		t.Fatalf("Builder omitted declared defaults: %v", got)
	}
	if !reflect.DeepEqual(work.Skills, before) {
		t.Fatal("presentation mutated the profile")
	}

	goal := agentworksproduct.BuiltinAgentProfile()
	goal.BuiltIn = true
	goalDefaults, err := agentProfileSkillDefaults(goal)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"builder", "run"} {
		if !reflect.DeepEqual(goalDefaults[mode], appendUniqueStrings(goal.Skills, agentworksproduct.ChatSkills(mode)...)) {
			t.Fatalf("Goal %s did not report runtime declarations", mode)
		}
	}
	relays, err := relayproduct.BuiltinAgentProfiles()
	if err != nil {
		t.Fatal(err)
	}
	relay := relays[0]
	relay.BuiltIn = true
	relayDefaults, err := agentProfileSkillDefaults(relay)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"builder", "run"} {
		definitions, err := relayproduct.ChatSkills(mode)
		if err != nil {
			t.Fatal(err)
		}
		names := append([]string{}, relay.Skills...)
		for _, skill := range definitions {
			names = appendUniqueStrings(names, skill.Name)
		}
		if !reflect.DeepEqual(relayDefaults[mode], names) {
			t.Fatalf("Flow %s did not report runtime declarations: %v", mode, relayDefaults)
		}
	}
	custom, err := agentProfileSkillDefaults(agentprofiles.Profile{ID: "work", Skills: []string{"my-own-skill"}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(custom, map[string][]string{"profile": {"my-own-skill"}}) {
		t.Fatalf("user profile acquired unrelated platform defaults: %v", custom)
	}
}

func TestProductDefaultSkillsHaveReadableModeSpecificDefinitions(t *testing.T) {
	t.Setenv("AGENT_PRODUCTS", "")
	for _, profile := range []agentprofiles.Profile{agentworksproduct.BuiltinAgentProfile(), workproduct.BuiltinAgentProfile()} {
		profile.BuiltIn = true
		if profile.ID == "work" {
			if err := workproduct.RegisterProductSkills(); err != nil {
				t.Fatal(err)
			}
		}
		defaults, err := agentProfileSkillDefaults(profile)
		if err != nil {
			t.Fatal(err)
		}
		definitions, err := agentProfileSkillDefinitions(profile, defaults, productpolicy.Selection{})
		if err != nil {
			t.Fatalf("%s: %v", profile.ID, err)
		}
		if len(definitions["builder"]) == 0 || len(definitions["run"]) == 0 {
			t.Fatalf("%s lacks readable defaults: %v", profile.ID, definitions)
		}
		if profile.ID == "agentworks" {
			for mode, names := range defaults {
				if len(definitions[mode]) != len(names) {
					t.Fatalf("Goal %s catalog omitted a configured runtime definition", mode)
				}
			}
		}
		for _, name := range defaults["builder"] {
			if !strings.HasPrefix(name, "panel-") {
				continue
			}
			if !skills.IsWorkspacePanelSkill(name) || slices.Contains(defaults["run"], name) {
				t.Fatalf("%s panel is missing or leaked setup into Run", name)
			}
			found := false
			for _, doc := range definitions["builder"] {
				if doc.FolderName == name {
					found = true
					if doc.Content != skills.BuiltinDefinition(name).Content {
						t.Fatalf("%s copied or changed the shared %s procedure", profile.ID, name)
					}
				}
			}
			if !found {
				t.Fatalf("%s Builder cannot read %s", profile.ID, name)
			}
		}
		for mode, docs := range definitions {
			for _, doc := range docs {
				if doc.Content == "" || doc.FolderName == "" || doc.FilePath != "Platform" {
					t.Fatalf("unreadable %s %s default", profile.ID, mode)
				}
				if !slices.Contains(defaults[mode], doc.FolderName) {
					t.Fatalf("catalog exposed a skill not declared in %s", mode)
				}
			}
		}
	}
}
