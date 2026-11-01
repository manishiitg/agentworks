package agentprofiles

import (
	"strings"
	"testing"
	"testing/fstest"
)

const baseManifest = `schema_version: 2
dependencies: {}
prompt:
  file: prompts/parent.md
  variables:
    NAME: Quill
profile:
  id: family-parent
  name: Parent
  version: 1
  runtime:
    transport: auto
    conversation:
      mode: singleton
    workspace:
      mode: fixed
      root: Chats
  built_in: true
`

func TestWorkingTipManifestRejectsInvalidDefinitions(t *testing.T) {
	for _, tips := range []string{
		"    - {id: missing-shared}\n",
		"    - {id: quick-switcher}\n    - {id: quick-switcher}\n",
		"    - {id: example, text: Hint, skip_if_used: unknown-feature}\n",
		"    - {id: example, text: Hint, when: {code_tabs: {min: 3, max: 1}}}\n",
		"    - {id: example, text: Hint, when: {script: run}}\n",
	} {
		if _, err := LoadProductManifest(manifestFS("ui:\n  tips:\n"+tips, nil), "product.yaml"); err == nil {
			t.Fatalf("invalid working tip accepted: %s", tips)
		}
	}
}

func manifestFS(extra string, files map[string]string) fstest.MapFS {
	fsys := fstest.MapFS{
		"product.yaml":      {Data: []byte(baseManifest + extra)},
		"prompts/parent.md": {Data: []byte("You are {{NAME}} for the parent. Today is {{TIME}}.")},
		"prompts/child.md":  {Data: []byte("You are {{NAME}} for the child.")},
	}
	for name, data := range files {
		fsys[name] = &fstest.MapFile{Data: []byte(data)}
	}
	return fsys
}

func TestLoadProductManifestSingleProfile(t *testing.T) {
	m, err := LoadProductManifest(manifestFS("", nil), "product.yaml")
	if err != nil {
		t.Fatal(err)
	}
	profiles, err := m.BuiltinProfiles(manifestFS("", nil), map[string]string{"TIME": "now"})
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 1 || profiles[0].ID != "family-parent" {
		t.Fatalf("profiles = %+v", profiles)
	}
	if got := profiles[0].SystemPromptTemplate; got != "You are Quill for the parent. Today is now." {
		t.Fatalf("prompt = %q", got)
	}
	if err := Validate(profiles[0]); err != nil {
		t.Fatalf("rendered profile should validate: %v", err)
	}
}

func TestLoadProductManifestExecutionFunctionTools(t *testing.T) {
	for name, extra := range map[string]string{
		"omitted":  "",
		"disabled": "execution:\n  function_tools:\n    tools: []\n",
		"enabled":  "execution:\n  function_tools:\n    category: workflow_calls\n    tools: [project_functions_list, project_functions_get_call]\n",
	} {
		t.Run(name, func(t *testing.T) {
			m, err := LoadProductManifest(manifestFS(extra, nil), "product.yaml")
			if err != nil {
				t.Fatal(err)
			}
			if name == "enabled" && (m.Execution.FunctionTools.Category != "workflow_calls" || len(m.Execution.FunctionTools.Tools) != 2) {
				t.Fatalf("lost execution admission: %+v", m.Execution.FunctionTools)
			}
		})
	}
	for name, extra := range map[string]string{
		"missing category": "tools: [project_functions_list]",
		"duplicate":        "category: functions\n    tools: [project_functions_list, project_functions_list]",
		"empty name":       "category: functions\n    tools: ['']",
		"untrimmed name":   "category: functions\n    tools: [' project_functions_list']",
		"unknown field":    "category: functions\n    toolz: [project_functions_list]",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := LoadProductManifest(manifestFS("execution:\n  function_tools:\n    "+extra+"\n", nil), "product.yaml"); err == nil {
				t.Fatal("invalid execution function declaration was accepted")
			}
		})
	}
}

const childProfile = `profiles:
  - id: family-child
    name: Child
    version: 1
    prompt:
      file: prompts/child.md
      variables:
        NAME: Quill
    tool_policy:
      mode: allowlist
      enabled: [execute_shell_command]
    runtime:
      transport: auto
      conversation:
        mode: keyed
        key_type: project
      workspace:
        mode: project
        projects_root: Chats/ExampleApp/activities
      capabilities:
        secrets: disabled
      sandbox:
        mode: strict
        network: disabled
        read_only: [Downloads]
    built_in: true
`

func TestLoadProductManifestMultipleProfiles(t *testing.T) {
	fsys := manifestFS(childProfile, nil)
	m, err := LoadProductManifest(fsys, "product.yaml")
	if err != nil {
		t.Fatal(err)
	}
	profiles, err := m.BuiltinProfiles(fsys, map[string]string{"TIME": "now"})
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 2 || profiles[1].ID != "family-child" {
		t.Fatalf("profiles = %+v", profiles)
	}
	child := profiles[1]
	if child.SystemPromptTemplate != "You are Quill for the child." {
		t.Fatalf("child prompt = %q", child.SystemPromptTemplate)
	}
	if !child.Runtime.Sandbox.IsStrict() || !child.Runtime.Sandbox.NetworkDisabled() || len(child.Runtime.Sandbox.ReadOnly) != 1 {
		t.Fatalf("child sandbox = %+v", child.Runtime.Sandbox)
	}
	if child.Runtime.Capabilities.Secrets != CapabilityDisabled {
		t.Fatalf("child secrets = %q", child.Runtime.Capabilities.Secrets)
	}
	for _, p := range profiles {
		if err := Validate(p); err != nil {
			t.Fatalf("%s: %v", p.ID, err)
		}
	}
	if _, ok := m.FindProfile("family-child"); !ok {
		t.Fatal("FindProfile should see the extra profile")
	}
}

func TestLoadProductManifestRejectsBadShapes(t *testing.T) {
	cases := map[string]struct {
		extra string
		files map[string]string
	}{
		"duplicate id": {extra: `profiles:
  - id: family-parent
    name: Dup
    version: 1
    prompt: {file: prompts/child.md}
`},
		"missing prompt": {extra: `profiles:
  - id: family-child
    name: Child
    version: 1
`},
		"unknown key": {extra: `profiles:
  - id: family-child
    name: Child
    version: 1
    prompt: {file: prompts/child.md}
    unknown_key: red
`},
		"missing prompt file": {extra: `profiles:
  - id: family-child
    name: Child
    version: 1
    prompt: {file: prompts/missing.md}
`},
		"chat duplicate external tools": {
			extra: `chat:
  run:
    prompt: {file: prompts/chat.md}
    skills: []
    external_tools: [list_workflows, list_workflows]
`,
			files: map[string]string{"prompts/chat.md": "chat prompt"},
		},
		"chat empty external tool": {
			extra: `chat:
  run:
    prompt: {file: prompts/chat.md}
    skills: []
    external_tools: [""]
`,
			files: map[string]string{"prompts/chat.md": "chat prompt"},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := LoadProductManifest(manifestFS(tc.extra, tc.files), "product.yaml"); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestLoadProductManifestChatExternalTools(t *testing.T) {
	extra := `chat:
  run:
    prompt: {file: prompts/chat.md}
    skills: []
    external_tools: [list_workflows, read_file]
`
	m, err := LoadProductManifest(manifestFS(extra, map[string]string{"prompts/chat.md": "chat prompt"}), "product.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if got := m.Chat["run"].ExternalTools; len(got) != 2 || got[0] != "list_workflows" || got[1] != "read_file" {
		t.Fatalf("external_tools = %v", got)
	}
}

func TestSandboxPolicyValidation(t *testing.T) {
	base := Profile{ID: "p", Name: "P", Version: 1, SystemPromptTemplate: "hi", BuiltIn: true, Runtime: RuntimePolicy{Transport: "auto"}}
	ok := base
	ok.Runtime.Sandbox = SandboxPolicy{Mode: "strict", Network: "disabled", ReadOnly: []string{"Downloads"}}
	if err := Validate(ok); err != nil {
		t.Fatal(err)
	}
	bad := []SandboxPolicy{
		{Mode: "jail"},
		{Network: "sometimes"},
		{Network: "disabled"}, // needs strict
		{Mode: "strict", ReadOnly: []string{"../etc"}},
		{Mode: "strict", ReadOnly: []string{"/abs"}},
	}
	for i, sb := range bad {
		p := base
		p.Runtime.Sandbox = sb
		if err := Validate(p); err == nil || !strings.Contains(err.Error(), "sandbox") {
			t.Fatalf("case %d should fail on sandbox, got %v", i, err)
		}
	}
}
