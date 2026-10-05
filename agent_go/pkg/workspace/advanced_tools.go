package workspace

import (
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// shellToolDef returns the execute_shell_command tool definition (single source of truth).
func shellToolDef() llmtypes.Tool {
	return llmtypes.Tool{
		Type: "function",
		Function: &llmtypes.FunctionDefinition{
			Name: "execute_shell_command",
			// The working directory is per-session and is NOT always the docs root:
			// a workshop tool-agent runs in its workflow folder, a step in its run
			// execution folder. Claiming the docs root here made agents prefix their
			// own workflow path onto every relative path and fail with a bare
			// "No such file or directory". Failed commands now report their cwd.
			Description: "Execute a shell command and return stdout, stderr, and exit code. Runs via shell (`sh -c`). The working directory is session-specific — often your workflow or run folder, not the workspace docs root — so run `pwd` before assuming, and never prefix a relative path with a directory you may already be inside. Any command that fails reports the directory it ran in. Absolute paths under the docs root are accepted. Other host paths are rejected unless the current session explicitly grants one read-only. With Local Chrome (CDP), use a supplied `~/Downloads/...` or `/Users/<user>/Downloads/...` path directly: the workspace `Downloads/` folder is separate and must not be substituted for the host path. Copy a granted host file into a writable workspace folder before modifying it. Installing packages works and persists: `pip install --user <pkg>`, `npm install -g <pkg>`, or a binary dropped into `$SANDBOX_PERSISTENT_DIR/bin` lands in this workflow's `.sandbox-cache/`, which every later run of the workflow reuses (a second `pip install --user` is 'already satisfied' with no network). Installed CLIs are already on PATH. `/tmp` is not available (it was shared with other users' agents): use `$TMPDIR` for scratch, which is private and removed after the command, and your workflow or Crew folder (e.g. `code/`) for anything that must last, such as repository clones. `$HOME` is private to this workflow or Crew, so git, ssh and CLI logins set there persist for it alone. Anything needing root/apt is not possible; ask for it to be installed on the host.",
			Parameters: llmtypes.NewParameters(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					// NOTE: use_shell was removed from the tool definition to simplify the interface for the LLM.
					// It is now hardcoded to true internally in ExecuteShellCommand.
					"command": map[string]interface{}{
						"type":        "string",
						"description": "Shell command to execute as a single string, including any arguments and shell operators.",
					},
					"timeout": map[string]interface{}{
						"type":        "integer",
						"description": "Optional timeout in seconds.",
					},
				},
				"required": []string{"command"},
			}),
		},
	}
}

// imageToolDef returns the read_image tool definition (single source of truth).
func imageToolDef() llmtypes.Tool {
	return llmtypes.Tool{
		Type: "function",
		Function: &llmtypes.FunctionDefinition{
			Name:        "read_image",
			Description: "Read an image file from the workspace and ask a question about it using a provider-backed vision model. provider and model_id are optional: leave both unset to use the workspace's configured image-analysis model. Set them only when you need a specific one, always together (a model_id without its provider is rejected). Where the list_llm_capabilities tool is available, list_llm_capabilities(capability=\"read_image\", include_models=true) is the source for a valid pair; if it is not available in this session, leave them unset.",
			Parameters: llmtypes.NewParameters(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"filepath": map[string]interface{}{
						"type":        "string",
						"description": "Full absolute path to the image file under the workspace docs root (e.g., '/Users/.../workspace-docs/_users/default/Chats/photo.png', '/app/workspace-docs/_users/default/Downloads/hdfc_login.png'). Workspace-relative paths are rejected. Absolute paths outside the workspace docs root are rejected.",
					},
					"query": map[string]interface{}{
						"type":        "string",
						"description": "Question to ask about the image (e.g., 'What is in this image?', 'Describe this image', 'What text is written here?')",
					},
					"provider": map[string]interface{}{
						"type":        "string",
						"description": "Optional image-analysis provider override. Leave unset to use the workspace default. If set, pass the matching model_id too. Where the list_llm_capabilities tool is available, list_llm_capabilities(capability=\"read_image\", include_models=true) lists the usable providers.",
					},
					"model_id": map[string]interface{}{
						"type":        "string",
						"description": "Optional image-analysis model id. Leave unset to use the workspace default. If set, pass the matching provider in the same call; take the pair from list_llm_capabilities(capability=\"read_image\", include_models=true) where that tool is available. Do not use tier labels such as low, medium, high, or auto as model IDs.",
					},
				},
				"required": []string{"filepath", "query"},
			}),
		},
	}
}

// generateTextLLMToolDef returns the generate_text_llm tool definition (single source of truth).
func generateTextLLMToolDef() llmtypes.Tool {
	return llmtypes.Tool{
		Type: "function",
		Function: &llmtypes.FunctionDefinition{
			Name:        "generate_text_llm",
			Description: "Generate text with the current workflow's tiered LLM configuration. High, medium, and low map to that workflow's tier_1, tier_2, and tier_3. The tool requires a current workflow and never uses a global tier configuration.",
			Parameters: llmtypes.NewParameters(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"user_message": map[string]interface{}{
						"type":        "string",
						"description": "The prompt to send to the selected tier model.",
					},
					"tier": map[string]interface{}{
						"type":        "string",
						"description": "Reasoning tier to use for text generation.",
						"enum":        []string{"high", "medium", "low"},
					},
				},
				"required": []string{"user_message", "tier"},
			}),
		},
	}
}

// diffPatchToolDef returns the diff_patch_workspace_file tool definition.
func diffPatchToolDef() llmtypes.Tool {
	return llmtypes.Tool{
		Type: "function",
		Function: &llmtypes.FunctionDefinition{
			Name:        "diff_patch_workspace_file",
			Description: "Apply a patch to workspace files and return the result: a unified diff for one file, or a Codex/Cursor \"*** Begin Patch\" patch for one or several files (all files are checked first and written together, or none). The filepath may be workspace-relative, an absolute path under the workspace docs root, or linked://<alias>/<relative-path> for an owner-approved read-write attached folder (attached-folder files one per call).",
			Parameters: llmtypes.NewParameters(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"filepath": map[string]interface{}{
						"type":        "string",
						"description": "Path to the file to patch (optional for a \"*** Begin Patch\" that names its own files). Accepts workspace-relative paths, absolute paths under the workspace docs root, and linked://<alias>/<relative-path> for an attached folder with read-write permission. Other absolute paths are rejected.",
					},
					"diff": map[string]interface{}{
						"type":        "string",
						"description": "Unified diff patch string to apply. The Codex/Cursor \"*** Begin Patch\" format is also accepted: \"*** Update File: <path>\" / \"*** Add File: <path>\" sections, hunks located by their context and optional @@ anchor line (no line numbers needed). A Begin Patch may change several files in one call: every file is checked first and all are written or none (filepath may then be omitted). Delete File and Move to are not supported.\n\nUnified diff format:\n- Headers: --- a/file and +++ b/file\n- Hunk headers: @@ -startLine,lineCount +startLine,lineCount @@\n- Context lines: ' ' prefix (space + content)\n- Removals: '-' prefix\n- Additions: '+' prefix\n- End with a trailing newline\n\nExample:\n--- a/file\n+++ b/file\n@@ -5,1 +5,1 @@\n-- [ ] task-1\n+- [x] task-1\n\nContext and removal lines must be byte-exact copies of the current file content, not retyped from memory. If the target text contains em dashes (—), curly quotes (“ ”), or arrows (→), do not hand-copy it — retyping commonly substitutes plain ASCII equivalents (-, \", ->) without you noticing, which this tool will correctly reject as a context mismatch. Read the file first and copy the exact bytes, or generate the diff programmatically from the file you just read.",
					},
				},
				// filepath may be omitted for a "*** Begin Patch" that names its files.
				"required": []string{"diff"},
			}),
		},
	}
}

// GetShellToolDefinitions returns only the shell (execute_shell_command) tool.
func GetShellToolDefinitions() []llmtypes.Tool {
	return []llmtypes.Tool{shellToolDef()}
}

// GetImageToolDefinitions returns image understanding tools.
func GetImageToolDefinitions() []llmtypes.Tool {
	return []llmtypes.Tool{imageToolDef()}
}

// GetGenerateTextLLMToolDefinitions returns only the text generation tool.
func GetGenerateTextLLMToolDefinitions() []llmtypes.Tool {
	return []llmtypes.Tool{generateTextLLMToolDef()}
}

// GetDiffPatchToolDefinitions returns only the diff_patch_workspace_file tool definition.
func GetDiffPatchToolDefinitions() []llmtypes.Tool {
	return []llmtypes.Tool{diffPatchToolDef()}
}

// GetAdvancedToolDefinitions returns the active agent-facing tools: shell,
// image understanding (read_image), text generation, and diff
// patch. Image/video/audio/music *generation* tools remain retired -- only
// read_image (pure inspection, no provider media creation) is active here.
func GetAdvancedToolDefinitions() []llmtypes.Tool {
	var tools []llmtypes.Tool
	tools = append(tools, GetShellToolDefinitions()...)
	tools = append(tools, GetImageToolDefinitions()...)
	tools = append(tools, GetGenerateTextLLMToolDefinitions()...)
	tools = append(tools, GetDiffPatchToolDefinitions()...)
	return tools
}
