package step_based_workflow

import (
	"fmt"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/guidance"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/skills"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// The system lists these identities; their procedures are read through
// read_skill or the native CLI skill projection only when relevant.
func init() {
	for _, skill := range []*llmtypes.Skill{
		{
			Name: "workflow-database", Description: "Read or update workflow database rows; includes managed-tool and saved-script helper procedures.",
			Content: "# Workflow database\n\nUse this skill when the task needs durable structured state. The system prompt's effective grant wins. Agents use query_workflow_db/mutate_workflow_db; saved scripts use agentworks_db. Never open SQLite files directly or change schema. Read db/README.md when granted. Open references/managed-read.md for querying, references/managed-write.md for permitted row changes, or references/script-helper.md for saved Python scripts. Schema changes belong to Builder.\n",
			SupportingFiles: []llmtypes.SkillFile{
				{RelPath: "references/managed-read.md", Content: []byte(guidance.StepSystemPromptTemplate("managed-db-read"))},
				{RelPath: "references/managed-write.md", Content: []byte(guidance.StepSystemPromptTemplate("managed-db-write"))},
				{RelPath: "references/script-helper.md", Content: []byte(workflowDBScriptHelper)},
			},
		},
		{
			Name: "workflow-knowledgebase", Description: "Read local workflow context and topic notes, or contribute verified notes under the granted paths.",
			Content: workflowKnowledgebaseProcedures,
		},
		{
			Name: "workflow-reflection", Description: "Record verified guidance after a workflow step succeeds; use only for the requested contribution stores.",
			Content: workflowReflectionProcedures,
		},
		{
			Name: "workflow-execution", Description: "Use the workflow HTTP tool bridge or author saved scripts; load only for code execution work.",
			Content: "# Workflow code execution\n\nFor HTTP tool calls, open builder-reference/references/mcp-bridge.md with read_skill; use the real registered schemas and injected MCP_CUSTOM/MCP_AUTH variables, never guess endpoints. For saved script authoring, open builder-reference/references/code-authoring.md and references/playwright-scripted.md only if browser automation is needed. Runtime paths, parameter contract and repair evidence are supplied in the core prompt. Read variables with os.environ['VAR_NAME'] and secrets with os.environ['SECRET_NAME']; never hardcode values or add defaults. STEP_OUTPUT_DIR already exists. Return real evidence; the controller runs canonical scripts after authoring.\n",
		},
	} {
		skill.Source = llmtypes.SkillSource{Origin: "builtin"}
		if err := skills.RegisterBuiltin(skill); err != nil {
			panic(fmt.Sprintf("register workflow skill: %v", err))
		}
	}
}

const workflowDBScriptHelper = `# Saved-script database helper

Use from agentworks_db import query, query_one, scalar, iter_query, execute,
insert, execute_many, transaction. Parameters use ? placeholders and rows are
dicts. execute_many imports batches; transaction groups changes atomically.
Give large reads an ORDER BY and stream with iter_query instead of loading all
rows. Read db/README.md for each table's ownership and business contract.
Never import sqlite3 or open DB_PATH directly, including sidecars. Never issue
CREATE/ALTER/DROP, recreate a table, or replace the whole table. Missing schema
is a blocker to report to Builder. Honor the effective access mode.
`

const workflowKnowledgebaseProcedures = `# Local workflow knowledgebase

Use only the actual knowledge paths and grants in the system prompt. Shared
Brain uses the brain skill and MCP tools; this guide does not authorize a local
fallback for a migrated workflow.

Read knowledgebase/context/context.md once at task start when it exists and is
granted: it contains user-supplied rules, preferences and domain context. Do not
edit context/. For discovered notes, read notes/_index.json first, then only the
relevant topics; never glob notes/*.md. Use the supplied absolute target or
WORKFLOW_KB alias, not a guessed current directory.

For permitted contributions, prefer an existing topic. Entity-scoped topics use
an entity slug (company-example.md); cross-cutting observations use pattern-<slug>.
Read the current topic, then append a dated section with diff_patch_workspace_file,
including when creating a topic. Never rewrite/delete earlier sections. No shell
redirection, heredocs, tee, Python or built-in file editors for KB content writes.
Capture concise verified observations from this execution; label uncertainty and
skip requested facts you cannot verify. Link related topics by slug.

After a note change patch notes/_index.json with size_bytes, section_count,
last_updated, last_updated_by and merged covers[] entity ids. You may calculate
values through shell/JQ, but apply the content edit through the patch tool.
The step is the direct writer; no post-step agent will supply missing notes.
`
