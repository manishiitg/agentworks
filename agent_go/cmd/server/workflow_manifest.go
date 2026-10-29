package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/browser"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/knowledgebase"
	step_based_workflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"

	"github.com/google/uuid"
	"github.com/manishiitg/mcpagent/mcpclient"
)

// Current manifest schema version. This is the JSON shape version.
const WorkflowManifestSchemaVersion = 1

var ErrInvalidWorkflowManifest = errors.New("manifest validation failed")

// WorkflowContractCurrentVersion is the product-managed workflow behavior
// contract version. Unlike schema_version, this gates agent-run workflow
// upgrades. An operator starts pending migrations from the workflow chat, and
// the version is stamped only after the workflow has been checked or migrated.
const WorkflowContractCurrentVersion = workflowContractSkillsVersion
const workflowContractSkillsVersion = step_based_workflow.WorkflowSkillsContractVersion
const workflowContractGoalAgentVersion = step_based_workflow.GoalAgentContractVersion

// Historical marker: per-schedule housekeeping choices are retired.
const workflowContractExplicitSchedulePulseVersion = "1.0.41"

const workflowContractRouteSummariesVersion = "1.0.40"

// Historical marker only: the scheduled 1.0.42 cleanup turn is retired, but
// workflows that already stamped it must remain on the known-version ladder.
const workflowContractRunScopedRoutesVersion = "1.0.42"

// Historical marker only: the scheduled 1.0.43 evaluation-retirement turn is
// retired. Keep recognizing already-stamped manifests without asking an older
// workflow to migrate forward into a contract that no longer exists.
const workflowContractEvalRetirementVersion = "1.0.43"

// Routed agents and scripted calls are nested beneath their owning Agent step.
// Workflows must migrate authored path assumptions and use the canonical code/
// source tree before execution can resume.
const workflowContractNestedAgentArtifactsVersion = step_based_workflow.NestedAgentArtifactsContractVersion

// Scripted steps reach the workflow database through the built-in agentworks_db
// helper, not by opening db.sqlite themselves (PLAT-428). The stamp is refused
// while any script still opens the database or carries a schema statement.
const workflowContractManagedDBScriptsVersion = step_based_workflow.ManagedDBScriptsContractVersion

// Every plan step description uses the standard section layout (## Goal,
// ## Inputs, ## Output, ## Rules, ## Done when, ## Guides). The stamp is refused
// while any plan step lacks Goal, Inputs, Output or Done when.
const workflowContractStepDescriptionLayoutVersion = step_based_workflow.StepDescriptionLayoutContractVersion

const workflowContractUnifiedAgentVersion = step_based_workflow.AgentStepContractVersion
const workflowContractDescriptionFirstVersion = step_based_workflow.AgentDescriptionFirstContractVersion

// Every agent description states its knowledge base, database, Brain and learnings
// use under ## Resources (definitions only; no grant changes).
const workflowContractResourceIntentVersion = step_based_workflow.ResourceIntentContractVersion

const workflowContractInitialVersion = "1.0.0"
const workflowContractAgentCodeVersion = "1.0.10"
const workflowContractPulseHistoryVersion = "1.0.11"
const workflowContractNotificationConfigVersion = "1.0.12"
const workflowContractHumanInputOwnershipVersion = "1.0.13"
const workflowContractHumanReadablePulseStateVersion = "1.0.14"
const workflowContractKBWriteMethodRetiredVersion = "1.0.15"
const workflowContractEvalVerdictSchemaVersion = "1.0.16"
const workflowContractCompactPulseReportVersion = "1.0.18"
const workflowContractLightweightPulseReportVersion = "1.0.19"
const workflowContractExecutivePulseJournalVersion = "1.0.20"
const workflowContractArtifactPurityVersion = "1.0.21"
const workflowContractLegacy122Version = "1.0.22"
const workflowContractDirectHTMLReportsVersion = "1.0.23"
const workflowContractScheduledRouteVersion = "1.0.24"
const workflowContractScheduleExecutionModelVersion = "1.0.25"
const workflowContractPeriodicPulseReviewVersion = "1.0.26"
const workflowContractDedicatedPulseScheduleVersion = "1.0.27"
const workflowContractSchedulePromptContractVersion = "1.0.28"
const workflowContractFinalizerOwnedScheduleVersion = "1.0.29"
const workflowContractReportActivitySectionVersion = "1.0.30"
const workflowContractReportActivityTabVersion = "1.0.31"
const workflowContractPulseLifecycleReconciliationVersion = "1.0.32"
const workflowContractPulseBacklogTriageVersion = "1.0.33"
const workflowContractPulseActionableBacklogVersion = "1.0.34"
const workflowContractOrchestratorStepTypeVersion = "1.0.35"
const workflowContractActivityTabFromRunSummaryVersion = "1.0.36"
const workflowContractScriptedPlanTypeVersion = "1.0.37"

// Historical contract markers used to identify retired workflow versions.
const workflowContractDeclaredExecutionModeRetiredVersion = "1.0.38"
const workflowContractDeclaredExecutionModeStrippedVersion = "1.0.39"

const (
	DefaultRunRetentionCount = 10
)

// WorkflowManifest is the top-level workflow.json structure that lives in each workspace.
type WorkflowManifest struct {
	Team                         *WorkflowTeam                       `json:"team,omitempty"`
	KnowledgebaseContractHistory json.RawMessage                     `json:"knowledgebase_contract_history,omitempty"`
	SharedKnowledgebase          []knowledgebase.Binding             `json:"shared_knowledgebase,omitempty"`
	KnowledgebaseMode            string                              `json:"knowledgebase_mode,omitempty"`
	BrainAccess                  string                              `json:"brain_access,omitempty"` // off, read or write (legacy "folders" reads as write); server-managed
	KnowledgebaseSources         []workflowtypes.KnowledgebaseSource `json:"knowledgebase_sources,omitempty"`
	CodeLayoutVersion            int                                 `json:"code_layout_version,omitempty"` // 0: legacy learnings; 1: persistent code tree
	SchemaVersion                int                                 `json:"schema_version"`
	ID                           string                              `json:"id"`
	// Kind distinguishes a Flow from a general workflow while reusing the
	// same manifest, schedules, access rules, runner, and run history.
	Kind              string `json:"kind,omitempty"`
	RelayRuntime      string `json:"relay_runtime,omitempty"`
	RelayDurability   string `json:"relay_durability,omitempty"`
	RelayOutputStepID string `json:"relay_output_step_id,omitempty"`
	Version           string `json:"version,omitempty"`
	Label             string `json:"label"`
	// Description is one short line about what the workflow is for. Shown next to the name in Slack's "who should answer"
	// picker, MCP lists and the Slack target list. Optional.
	Description      string               `json:"description,omitempty"`
	ChatInstructions string               `json:"chat_instructions,omitempty"`
	Icon             string               `json:"icon,omitempty"`
	Capabilities     WorkflowCapabilities `json:"capabilities"`
	Schedules        []WorkflowSchedule   `json:"schedules"`
	// CreatedBy is the user ID that created this workflow, stamped once at
	// creation time (handleCreateWorkflowManifest) from the authenticated
	// request. Scheduled/cron runs have no logged-in user of their own --
	// this is how they resolve which account's stored secrets
	// ($SECRET_<NAME>) they should actually see, instead of silently
	// falling through to the placeholder "default" user, who never has any
	// secrets stored (nobody configures a workflow's API keys while logged
	// in as "default"). Confirmed live on the Dominion deployment
	// 2026-08-31/09-01: real Polygon/Finnhub keys existed under the actual
	// creating user's account the whole time; scheduled runs simply never
	// looked there. Empty for any workflow created before this field
	// existed -- those keep today's "default" fallback until backfilled.
	CreatedBy string `json:"created_by,omitempty"`
	// Access is who owns and who may read this workflow (workflow_access.go).
	// Absent on legacy manifests, where CreatedBy alone names the owner, or
	// nobody does and the account tier applies.
	Access               *WorkflowAccess                             `json:"access,omitempty"`
	CreatedAt            string                                      `json:"created_at,omitempty"`
	UpdatedAt            string                                      `json:"updated_at,omitempty"`
	FolderAccess         []workflowtypes.WorkflowFolderGrant         `json:"folder_access,omitempty"`
	FolderAccessRequests []workflowtypes.WorkflowFolderAccessRequest `json:"folder_access_requests,omitempty"`
	// CrewAttachments exposes Crew project workspaces under stable aliases,
	// read-only. Unlike FolderAccess (owner-approved host directories),
	// attachments reference workspace paths resolved at attach time.
	CrewAttachments []workflowtypes.CrewAttachment `json:"crew_attachments,omitempty"`
	// WorkflowContextPaths are durable links to other workflows. They are
	// resolved through the same authorization boundary as transient # references
	// and are always mounted read-only.
	WorkflowContextPaths []string `json:"workflow_context_paths,omitempty"`
	// InstalledPlaybooks are Builder-only setup guides materialized as
	// workflow-local skills. Execution agents continue to use the workflow and
	// per-step runtime skill selections.
	InstalledPlaybooks []InstalledPlaybook `json:"installed_playbooks,omitempty"`

	// PaceOnLowQuota spreads a run out rather than racing into a provider
	// capacity wall. Opt-in per workflow, because it is only ever the right
	// trade for workflows whose consumption is large relative to the provider
	// window — everything else pays wall-clock for nothing.
	//
	// It does not reduce what a run consumes; it moves consumption across a
	// window reset. That is why it is a wait-until-reset rather than a fixed
	// delay: padding every step wastes time when quota is healthy and still
	// does not save a run whose next step is the one that exhausts the window.
	PaceOnLowQuota bool `json:"pace_on_low_quota,omitempty"`

	// PaceThresholdPercent is the window usage at or above which the next step
	// waits. Zero means the default.
	PaceThresholdPercent int `json:"pace_threshold_percent,omitempty"`

	// Backup is declarative configuration for builder-agent managed backup.
	// Operational status is written separately to backup/status.json so normal
	// backup attempts do not churn workflow.json.
	Backup *WorkflowBackupConfig `json:"backup,omitempty"`

	Publish *WorkflowPublishConfig `json:"publish,omitempty"`

	// MalformedConfig lists optional config blocks (e.g. "backup", "publish") that
	// failed to parse and were dropped so the workflow could still load. Transient
	// (never serialized): set during ReadWorkflowManifest, used to avoid clobbering
	// the on-disk config on write-back and to flag the issue.
	MalformedConfig []string `json:"-"`
}

type InstalledPlaybook struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Version     string `json:"version"`
	Category    string `json:"category"`
	SkillName   string `json:"skill_name"`
	SourceHash  string `json:"source_hash"`
	Status      string `json:"status"`
	InstalledAt string `json:"installed_at"`
}

type WorkflowBackupConfig struct {
	Enabled      bool                        `json:"enabled"`
	Mode         string                      `json:"mode,omitempty"` // "agent" (default)
	AfterRuns    bool                        `json:"after_runs"`
	Destinations []WorkflowBackupDestination `json:"destinations,omitempty"`
	Notes        string                      `json:"notes,omitempty"`
}

type WorkflowBackupDestination struct {
	ID         string   `json:"id"`
	Type       string   `json:"type"`     // git, object_store, huggingface, local_zip
	Provider   string   `json:"provider"` // github, git, r2, s3, b2, huggingface, local
	Repo       string   `json:"repo,omitempty"`
	Branch     string   `json:"branch,omitempty"`
	Bucket     string   `json:"bucket,omitempty"`
	Prefix     string   `json:"prefix,omitempty"`
	Covers     []string `json:"covers,omitempty"`
	SecretRefs []string `json:"secret_refs,omitempty"`
	Notes      string   `json:"notes,omitempty"`
}

// WorkflowPublishConfig is declarative config for publishing the workflow's HTML
// artifacts to a public URL. Provider-agnostic: the destination's provider is a
// free-form string and the per-host deploy logic lives in the publish-strategy
// reference doc, not in Go.
// NOTE: this config is authored by the builder agent, so its sub-fields are kept
// deliberately tolerant. Free-form / variable-shape fields (e.g. targets, which the
// agent may write as plain strings OR rich objects) use json.RawMessage so a shape
// the agent chose can never fail manifest parsing and drop the whole workflow.
type WorkflowPublishConfig struct {
	Enabled       bool                         `json:"enabled"`
	Mode          string                       `json:"mode,omitempty"`           // "agent" (default)
	Targets       []json.RawMessage            `json:"targets,omitempty"`        // report targets, as strings or objects
	DashboardMode string                       `json:"dashboard_mode,omitempty"` // "snapshot" (static HTML)
	URL           string                       `json:"url,omitempty"`            // last published URL (agent-written, mirror of status)
	AfterRuns     bool                         `json:"after_runs"`
	Destinations  []WorkflowPublishDestination `json:"destinations,omitempty"`
	Notes         string                       `json:"notes,omitempty"`
}

type WorkflowPublishDestination struct {
	ID            string   `json:"id"`
	Provider      string   `json:"provider"`                  // free-form: netlify, vercel, cloudflare-pages, github-pages, s3, ...
	Method        string   `json:"method,omitempty"`          // cli | git | sync
	Site          string   `json:"site,omitempty"`            // project / site / bucket / repo identifier
	SecretName    string   `json:"secret_name,omitempty"`     // global secret holding the deploy token (CI only)
	Visibility    string   `json:"visibility,omitempty"`      // public | private | unguessable-link (agent's choice)
	PublicBaseURL string   `json:"public_base_url,omitempty"` // filled in by the agent after first deploy
	URL           string   `json:"url,omitempty"`             // this destination's published URL
	Covers        []string `json:"covers,omitempty"`
	Notes         string   `json:"notes,omitempty"`
}

// WorkflowCapabilities stores workflow-wide agent and tool configuration.
type WorkflowCapabilities struct {
	SelectedServers []string `json:"selected_servers"`
	// ChosenVaultAppsOnly is "Use all my apps automatically" switched off: only the Vault apps in SelectedServers
	// are used. Off (the default) means every Vault app the person may use (owner decision 2026-10-27, PLAT-856-394).
	ChosenVaultAppsOnly       bool                           `json:"chosen_vault_apps_only,omitempty"`
	SelectedTools             []string                       `json:"selected_tools"`
	SelectedSkills            []string                       `json:"selected_skills"`
	SelectedSecrets           []string                       `json:"selected_secrets"`
	SelectedGlobalSecretNames *[]string                      `json:"selected_global_secret_names"` // nil = none, [] = none
	BrowserMode               string                         `json:"browser_mode"`
	BrowserCDPPort            int                            `json:"browser_cdp_port,omitempty"`
	UseCodeExecutionMode      bool                           `json:"use_code_execution_mode"`
	LLMConfig                 *workflowtypes.PresetLLMConfig `json:"llm_config,omitempty"`
	Notifications             *WorkflowNotificationConfig    `json:"notifications,omitempty"`
	// SlackConnectionID selects WHICH configured Slack app identity this
	// workflow talks through — for bot replies, tool sends, and
	// notifications alike. Empty means "inherit the platform default
	// connection", so existing workflows are unaffected. An unknown or
	// disabled connection fails the send: it never falls back to another
	// identity, because delivering from an unintended Slack app is worse
	// than not delivering. An identifier, never a secret.
	SlackConnectionID string `json:"slack_connection_id,omitempty"`
	// NativeAgentTools ("Native agent tools") runs this workflow's Builder and
	// Run-mode chats in agent_tools mode full: the coding CLI's own tools in a
	// sandbox limited to the chat's folders (mcp_only when it cannot be
	// confined), with protected files refused. Interactive chats of owners and editors only: step
	// agents, schedules, webhooks, bots and read-only users keep
	// AgentWorks-only tools. Always on (2026-10-03): the field is kept so older
	// manifests still load, and a stored false is ignored. Read it through NativeAgentToolsEnabled.
	NativeAgentTools *bool `json:"native_agent_tools,omitempty"`
}

// NativeAgentToolsEnabled reports the "Native agent tools" switch, which is on
// unless it was explicitly turned off.
func (c WorkflowCapabilities) NativeAgentToolsEnabled() bool {
	return nativeAgentToolsEnabled(c.NativeAgentTools)
}

// nativeAgentToolsEnabled: native agent tools are on for every workflow, Flow, Crew and Code (owner, 2026-10-03). A value an older workflow
// saved as false is ignored, as it already is for a Crew or a Code: there is no switch left to turn it back on.
func nativeAgentToolsEnabled(_ *bool) bool {
	return true
}

// WorkflowNotificationConfig contains only safe references. Credential values
// are kept in the encrypted secret store and resolved immediately before a run.
// WorkflowWakeTab names one tab of one Crew.
type WorkflowWakeTab struct {
	CrewID string `json:"crew_id"`
	ChatID string `json:"chat_id"`
}

type WorkflowNotificationConfig struct {
	RunSummaryInstructions string   `json:"run_summary_instructions,omitempty"`
	RunSummaryChannels     []string `json:"run_summary_channels,omitempty"`
	// Explicit team recipients. Empty disables summary email delivery.
	RunSummaryRecipients []string `json:"run_summary_recipients,omitempty"`
	// Sender selected from the accounts already added to this product.
	GmailConnectionID string           `json:"gmail_connection_id,omitempty"`
	ExcludeChannels   []string         `json:"exclude_channels,omitempty"`
	BreakAlerts       *bool            `json:"break_alerts,omitempty"`
	WakeTab           *WorkflowWakeTab `json:"wake_tab,omitempty"`
}

func (c *WorkflowNotificationConfig) BreakAlertsEnabled() bool {
	return c == nil || c.BreakAlerts == nil || *c.BreakAlerts
}

const defaultRunSummaryInstructions = "Before external delivery, read prior run summaries with get_notification_history. Always record the current run summary in Activity. Send Slack, Gmail, or WhatsApp only when the current run contains a new materially important outcome, failure, recovery, blocker, decision, or threshold crossing compared with prior summaries. Ignore timestamps, routine counts, and wording-only differences. If nothing important changed, call record_summary with delivery=record_only."

func (c *WorkflowNotificationConfig) EffectiveRunSummaryInstructions() string {
	if c == nil {
		return ""
	}
	if value := strings.TrimSpace(c.RunSummaryInstructions); value != "" {
		return value
	}
	return ""
}

func runSummaryInstructionsOrDefault(c *WorkflowNotificationConfig) string {
	if value := c.EffectiveRunSummaryInstructions(); value != "" {
		return value
	}
	return defaultRunSummaryInstructions
}

// WorkflowSchedule represents a clock or webhook trigger stored in the manifest.
type WorkflowSchedule struct {
	RunAs              string                 `json:"-"` // Server-bound caller from the common schedule record.
	TeamAuthorizedBy   string                 `json:"team_authorized_by,omitempty"`
	TeamPolicyRevision int                    `json:"team_policy_revision,omitempty"`
	ID                 string                 `json:"id"`
	Name               string                 `json:"name"`
	Description        string                 `json:"description,omitempty"`
	ScheduleType       string                 `json:"schedule_type,omitempty"` // "cron" (default), "calendar", or "webhook"
	CronExpression     string                 `json:"cron_expression"`
	Timezone           string                 `json:"timezone"`
	Enabled            bool                   `json:"enabled"`
	TriggerPayload     json.RawMessage        `json:"trigger_payload,omitempty"`
	CalendarItems      []CalendarScheduleItem `json:"calendar_items,omitempty"`
	GroupNames         []string               `json:"group_names,omitempty"`
	// RouteSelections selects deterministic routing-step branches for the scheduled
	// full workflow. It is the same shape accepted by run_full_workflow; keeping it
	// as data prevents a schedule from becoming a second, free-text workflow.
	RouteSelections map[string]string      `json:"route_selections,omitempty"`
	Webhook         *WorkflowWebhookConfig `json:"webhook,omitempty"`
	Kind            string                 `json:"kind,omitempty"`
	Caller          *triggerCaller         `json:"caller,omitempty"`
	// Function makes a kind=function trigger a callable workflow function.
	Function *WorkflowFunctionSpec `json:"function,omitempty"`
	Mode     string                `json:"mode,omitempty"`     // "workshop" for workflow schedules; legacy "workflow" is normalized at runtime
	Messages []string              `json:"messages,omitempty"` // Predefined message queue for workshop schedules (sent one-by-one)
	// DirectMessagesReason records why a schedule-local conversation is preferable
	// to a canonical route despite its weaker step-level lifecycle.
	WorkshopMode         string `json:"workshop_mode,omitempty"` // Scheduled session mode: workshop or run.
	DirectMessagesReason string `json:"direct_messages_reason,omitempty"`
	Query                string `json:"query,omitempty"`           // Message to execute (multi-agent mode)
	ResumePrevious       *bool  `json:"resume_previous,omitempty"` // Coding-agent CLI only: resume the latest prior thread (same provider) instead of a fresh session each run. nil = default (fresh session); explicit true opts in.
	ExecutionMode        string `json:"execution_mode,omitempty"`
	// CollisionPolicy controls what happens when the workflow is already owned
	// by another scheduled/execution run. Empty/"skip" preserves the default;
	// "queue_latest" durably retains the newest occurrence for a later retry.
	CollisionPolicy string `json:"collision_policy,omitempty"`
	// ConcurrencyMode is sequential by default. Parallel is an explicit,
	// human-approved risk opt-in for an independently runnable schedule.
	ConcurrencyMode          string `json:"concurrency_mode,omitempty"`
	ParallelRiskAcknowledged bool   `json:"parallel_risk_acknowledged,omitempty"`
	// MaxStartDelayMinutes bounds a queued occurrence. Zero uses the platform
	// default. It prevents a stale market-hours or notification run from firing
	// much later with obsolete assumptions.
	MaxStartDelayMinutes int `json:"max_start_delay_minutes,omitempty"`
	// AfterScheduleID expresses a completion dependency without encoding it as
	// a fragile clock offset. It is retained as the backward-compatible singular
	// form of AfterScheduleIDs.
	AfterScheduleID string `json:"after_schedule_id,omitempty"`
	// AfterScheduleIDs lists completion dependencies. The dependent occurrence
	// waits for every named schedule's durable occurrence on the same local date.
	AfterScheduleIDs []string `json:"after_schedule_ids,omitempty"`
	// AfterTerminalStatus controls which prerequisite outcomes release this
	// schedule. Empty/"completed" requires a successful completion; any_terminal
	// permits failed, partial, stopped, or interrupted prerequisites too.
	AfterTerminalStatus string `json:"after_terminal_status,omitempty"`
	// AfterDelayMinutes delays release after the prerequisite's terminal receipt.
	AfterDelayMinutes int `json:"after_delay_minutes,omitempty"`
	// DependencyDeadline is an optional HH:MM local-time deadline. A prerequisite
	// that has not released this occurrence by then expires visibly instead of
	// leaking into the next operating day.
	DependencyDeadline string `json:"dependency_deadline,omitempty"`
}

// ShouldResumePrevious reports whether a scheduled run should resume the
// workflow's latest coding-agent thread. Resume is opt-in: omitted/null means
// each scheduled run starts fresh.
func (s WorkflowSchedule) ShouldResumePrevious() bool {
	return s.ResumePrevious != nil && *s.ResumePrevious
}

// scheduleDependencyIDs returns the de-duplicated union of the legacy singular
// dependency and the current list form, preserving declaration order.
func scheduleDependencyIDs(schedule WorkflowSchedule) []string {
	seen := make(map[string]struct{}, len(schedule.AfterScheduleIDs)+1)
	dependencies := make([]string, 0, len(schedule.AfterScheduleIDs)+1)
	appendID := func(raw string) {
		id := strings.TrimSpace(raw)
		if id == "" {
			return
		}
		if _, exists := seen[id]; exists {
			return
		}
		seen[id] = struct{}{}
		dependencies = append(dependencies, id)
	}
	appendID(schedule.AfterScheduleID)
	for _, id := range schedule.AfterScheduleIDs {
		appendID(id)
	}
	return dependencies
}

type CalendarScheduleItem struct {
	ID             string          `json:"id,omitempty"`
	Date           string          `json:"date"` // YYYY-MM-DD in schedule timezone
	Time           string          `json:"time"` // HH:MM in schedule timezone
	Description    string          `json:"description,omitempty"`
	TriggerPayload json.RawMessage `json:"trigger_payload,omitempty"`
	Messages       []string        `json:"messages,omitempty"`
}

func validateScheduleRuntimePolicy(schedule WorkflowSchedule) error {
	if err := validateWebhookSchedule(schedule); err != nil {
		return err
	}
	switch strings.TrimSpace(schedule.ExecutionMode) {
	case "", "close_only":
	default:
		return fmt.Errorf("execution_mode must be empty or close_only")
	}
	switch strings.TrimSpace(schedule.CollisionPolicy) {
	case "", "skip", "queue_latest", "retry", "coalesce":
	default:
		return fmt.Errorf("collision_policy must be skip, queue_latest, retry, or coalesce")
	}
	switch strings.TrimSpace(schedule.ConcurrencyMode) {
	case "", "sequential":
	case "parallel":
		if !schedule.ParallelRiskAcknowledged {
			return fmt.Errorf("parallel_risk_acknowledged=true is required when concurrency_mode=parallel")
		}
		if schedule.ScheduleType == "webhook" {
			return fmt.Errorf("concurrency_mode=parallel is only supported for producing cron/calendar schedules")
		}
	default:
		return fmt.Errorf("concurrency_mode must be sequential or parallel")
	}
	if schedule.MaxStartDelayMinutes < 0 {
		return fmt.Errorf("max_start_delay_minutes cannot be negative")
	}
	for i, rawID := range schedule.AfterScheduleIDs {
		if strings.TrimSpace(rawID) == "" {
			return fmt.Errorf("after_schedule_ids[%d] cannot be empty", i)
		}
	}
	for _, dependencyID := range scheduleDependencyIDs(schedule) {
		if dependencyID == schedule.ID {
			return fmt.Errorf("schedule dependencies cannot reference the schedule itself")
		}
	}
	switch strings.TrimSpace(schedule.AfterTerminalStatus) {
	case "", "completed", "any_terminal":
	default:
		return fmt.Errorf("after_terminal_status must be completed or any_terminal")
	}
	if schedule.AfterDelayMinutes < 0 {
		return fmt.Errorf("after_delay_minutes cannot be negative")
	}
	if deadline := strings.TrimSpace(schedule.DependencyDeadline); deadline != "" {
		if _, err := time.Parse("15:04", deadline); err != nil {
			return fmt.Errorf("dependency_deadline must be HH:MM in the schedule timezone")
		}
	}
	if len(scheduleDependencyIDs(schedule)) == 0 &&
		(strings.TrimSpace(schedule.AfterTerminalStatus) != "" || schedule.AfterDelayMinutes != 0 || strings.TrimSpace(schedule.DependencyDeadline) != "") {
		return fmt.Errorf("after_terminal_status, after_delay_minutes, and dependency_deadline require after_schedule_id or after_schedule_ids")
	}
	return nil
}

func scheduleAllowsParallel(schedule WorkflowSchedule) bool {
	return strings.TrimSpace(schedule.ConcurrencyMode) == "parallel" && schedule.ParallelRiskAcknowledged && schedule.ScheduleType != "webhook"
}

func effectiveScheduleConcurrencyMode(schedule WorkflowSchedule) string {
	if scheduleAllowsParallel(schedule) {
		return "parallel"
	}
	return "sequential"
}

// --- Validation ---

// ValidateManifest checks that a WorkflowManifest has required fields and valid values.
func ValidateManifest(m *WorkflowManifest) error {
	if m == nil {
		return fmt.Errorf("manifest is required")
	}
	reserved := []string{}
	for _, source := range m.KnowledgebaseSources {
		reserved = append(reserved, source.Alias)
	}
	for _, attachment := range m.CrewAttachments {
		reserved = append(reserved, attachment.Alias)
	}
	if err := knowledgebase.ValidateBindings(m.SharedKnowledgebase, reserved); err != nil {
		return err
	}
	if m.KnowledgebaseMode != "" && m.KnowledgebaseMode != "shared" {
		return fmt.Errorf("unknown knowledgebase_mode")
	}
	if m.KnowledgebaseMode == "shared" && len(m.SharedKnowledgebase) == 0 {
		return fmt.Errorf("shared knowledge requires a binding")
	}
	if m != nil {
		if err := workflowtypes.ValidateKnowledgebaseSources(m.KnowledgebaseSources, m.ID); err != nil {
			return err
		}
	}
	if m.CodeLayoutVersion < 0 || m.CodeLayoutVersion > 1 {
		return fmt.Errorf("unsupported code_layout_version %d", m.CodeLayoutVersion)
	}
	if m.SchemaVersion < 1 {
		return fmt.Errorf("schema_version must be >= 1")
	}
	if m.ID == "" {
		return fmt.Errorf("id is required")
	}
	if m.Kind != "" && m.Kind != "relay" {
		return fmt.Errorf("kind must be relay or omitted")
	}
	if m.RelayRuntime != "" && (m.Kind != "relay" || m.RelayRuntime != "python") {
		return fmt.Errorf("relay_runtime must be python on a Flow")
	}
	if m.RelayDurability != "" && (!isPythonRelay(m) || m.RelayDurability != "dbos") {
		return fmt.Errorf("relay_durability must be dbos on a Python Flow")
	}
	if isPythonRelay(m) && m.RelayOutputStepID != "" {
		return fmt.Errorf("Python Flows return run(INPUT, ctx); relay_output_step_id is not supported")
	}
	if m.Kind != "relay" && m.RelayOutputStepID != "" {
		return fmt.Errorf("relay_output_step_id requires kind relay")
	}

	if m.Kind == "relay" && strings.TrimSpace(m.Capabilities.SlackConnectionID) != "" {
		return fmt.Errorf("Flows do not support Slack connections")
	}
	if m.Kind == "relay" && m.Capabilities.Notifications != nil {
		notifications := m.Capabilities.Notifications
		unsupportedChannel := func(channels []string) bool {
			return slices.ContainsFunc(channels, func(channel string) bool {
				return strings.EqualFold(strings.TrimSpace(channel), "whatsapp") || strings.EqualFold(strings.TrimSpace(channel), "slack")
			})
		}
		if unsupportedChannel(notifications.RunSummaryChannels) {
			return fmt.Errorf("Flows do not support Slack or WhatsApp notifications")
		}
	}
	if m.Label == "" {
		return fmt.Errorf("label is required")
	}
	if reason := workflowtypes.ValidateIcon(m.Icon); reason != "" {
		return fmt.Errorf("%s", reason)
	}
	seenFolderGrantIDs := make(map[string]struct{}, len(m.FolderAccess))
	seenFolderGrantAliases := make(map[string]struct{}, len(m.FolderAccess))
	seenFolderGrantEnvKeys := make(map[string]struct{}, len(m.FolderAccess))
	for i, grant := range m.FolderAccess {
		grant.ID = strings.TrimSpace(grant.ID)
		grant.Alias = strings.TrimSpace(grant.Alias)
		grant.Path = strings.TrimSpace(grant.Path)
		grant.Access = strings.TrimSpace(grant.Access)
		if grant.ID == "" {
			return fmt.Errorf("folder_access[%d].id is required", i)
		}
		if _, exists := seenFolderGrantIDs[grant.ID]; exists {
			return fmt.Errorf("duplicate folder_access id %q", grant.ID)
		}
		seenFolderGrantIDs[grant.ID] = struct{}{}
		if !validWorkflowFolderAlias(grant.Alias) {
			return fmt.Errorf("folder_access[%d].alias must start with a letter or number and contain only letters, numbers, hyphens, or underscores", i)
		}
		aliasKey := strings.ToLower(grant.Alias)
		if _, exists := seenFolderGrantAliases[aliasKey]; exists {
			return fmt.Errorf("duplicate folder_access alias %q", grant.Alias)
		}
		seenFolderGrantAliases[aliasKey] = struct{}{}
		envKey := workflowFolderAliasEnvKey(grant.Alias)
		if _, exists := seenFolderGrantEnvKeys[envKey]; exists {
			return fmt.Errorf("folder_access[%d].alias collides with another attached-folder environment key", i)
		}
		seenFolderGrantEnvKeys[envKey] = struct{}{}
		if !filepath.IsAbs(grant.Path) {
			return fmt.Errorf("folder_access[%d].path must be absolute", i)
		}
		if strings.ContainsRune(grant.Path, '\x00') {
			return fmt.Errorf("folder_access[%d].path contains a NUL byte", i)
		}
		cleanPath := filepath.Clean(grant.Path)
		if cleanPath == filepath.VolumeName(cleanPath)+string(filepath.Separator) {
			return fmt.Errorf("folder_access[%d].path cannot be a filesystem root", i)
		}
		switch grant.Access {
		case workflowtypes.FolderAccessReadOnly, workflowtypes.FolderAccessReadWrite:
		default:
			return fmt.Errorf("folder_access[%d].access must be read_only or read_write", i)
		}
	}
	seenFolderRequestIDs := make(map[string]struct{}, len(m.FolderAccessRequests))
	for i, request := range m.FolderAccessRequests {
		if strings.TrimSpace(request.ID) == "" {
			return fmt.Errorf("folder_access_requests[%d].id is required", i)
		}
		if _, exists := seenFolderRequestIDs[request.ID]; exists {
			return fmt.Errorf("duplicate folder_access request id %q", request.ID)
		}
		seenFolderRequestIDs[request.ID] = struct{}{}
		if !validWorkflowFolderAlias(strings.TrimSpace(request.Alias)) {
			return fmt.Errorf("folder_access_requests[%d].alias is invalid", i)
		}
		switch strings.TrimSpace(request.Access) {
		case workflowtypes.FolderAccessReadOnly, workflowtypes.FolderAccessReadWrite:
		default:
			return fmt.Errorf("folder_access_requests[%d].access must be read_only or read_write", i)
		}
		if strings.TrimSpace(request.Reason) == "" {
			return fmt.Errorf("folder_access_requests[%d].reason is required", i)
		}
		if request.RequestedPath != "" {
			if strings.ContainsRune(request.RequestedPath, '\x00') {
				return fmt.Errorf("folder_access_requests[%d].requested_path contains a NUL byte", i)
			}
			if !filepath.IsAbs(request.RequestedPath) {
				return fmt.Errorf("folder_access_requests[%d].requested_path must be absolute", i)
			}
			cleanPath := filepath.Clean(request.RequestedPath)
			if cleanPath == filepath.VolumeName(cleanPath)+string(filepath.Separator) {
				return fmt.Errorf("folder_access_requests[%d].requested_path cannot be a filesystem root", i)
			}
		}
	}
	seenWorkflowContexts := make(map[string]struct{}, len(m.WorkflowContextPaths))
	for i, raw := range m.WorkflowContextPaths {
		path := strings.TrimSuffix(strings.TrimSpace(raw), "/")
		parts := strings.Split(path, "/")
		if len(parts) != 2 || parts[0] != "Workflow" || parts[1] == "" || parts[1] == "." || parts[1] == ".." || strings.ContainsAny(path, "\\\x00") {
			return fmt.Errorf("workflow_context_paths[%d] must be a Workflow/<folder> path", i)
		}
		if _, exists := seenWorkflowContexts[path]; exists {
			return fmt.Errorf("duplicate workflow_context_paths entry %q", path)
		}
		seenWorkflowContexts[path] = struct{}{}
	}

	if m.Capabilities.BrowserMode != "" {
		if err := browser.ValidateMode(m.Capabilities.BrowserMode); err != nil {
			return err
		}
	}

	// Hyphen and underscore server names are compatibility aliases throughout
	// MCP resolution. Reject equivalent duplicates here so a workflow cannot
	// accidentally launch the same configured server more than once.
	seenSelectedServers := make(map[string]string, len(m.Capabilities.SelectedServers))
	for _, serverName := range m.Capabilities.SelectedServers {
		trimmed := strings.TrimSpace(serverName)
		if trimmed == "" {
			return fmt.Errorf("capabilities.selected_servers cannot contain an empty server name")
		}
		aliasKey := strings.ReplaceAll(trimmed, "_", "-")
		if existing, duplicate := seenSelectedServers[aliasKey]; duplicate {
			return fmt.Errorf("duplicate capabilities.selected_servers entries %q and %q resolve to the same MCP server", existing, serverName)
		}
		seenSelectedServers[aliasKey] = serverName
	}

	// Validate LLM config if present
	if m.Capabilities.LLMConfig != nil {
		if err := workflowtypes.ValidatePresetLLMConfigPublic(m.Capabilities.LLMConfig); err != nil {
			return fmt.Errorf("invalid llm_config: %w", err)
		}
	}

	// Validate schedules
	scheduleIDs := make(map[string]struct{}, len(m.Schedules))
	for _, schedule := range m.Schedules {
		if id := strings.TrimSpace(schedule.ID); id != "" {
			scheduleIDs[id] = struct{}{}
		}
	}
	for i, sched := range m.Schedules {
		if err := validateRouteScheduleMessages(sched); err != nil {
			return fmt.Errorf("schedules[%d]: %w", i, err)
		}
		if isPythonRelay(m) && (len(sched.RouteSelections) != 0 || (sched.Webhook != nil && (strings.TrimSpace(sched.Webhook.StepID) != "" || sched.Webhook.PayloadMappings != nil))) {
			return fmt.Errorf("Python Flows run relay.py; workflow step targets and payload routing are not supported")
		}
		if m.Kind == "relay" && !sched.IsFunctionTrigger() {
			return fmt.Errorf("schedules[%d]: Flows support API function triggers only; cron and calendar schedules are not supported", i)
		}
		if m.Kind == "relay" && len(normalizeScheduleGroupNames(sched.GroupNames)) > 1 {
			return fmt.Errorf("schedules[%d]: a legacy Flow trigger may bind at most one configuration group", i)
		}
		if sched.ID == "" {
			return fmt.Errorf("schedules[%d].id is required", i)
		}
		if scheduleTypeOrDefault(sched.ScheduleType) == "cron" && sched.CronExpression == "" {
			return fmt.Errorf("schedules[%d].cron_expression is required", i)
		}
		if scheduleTypeOrDefault(sched.ScheduleType) == "calendar" && len(sched.CalendarItems) == 0 {
			return fmt.Errorf("schedules[%d].calendar_items is required for calendar schedules", i)
		}
		// group_names required for workflow/workshop modes, not for multi-agent
		if m.Kind != "relay" && sched.Mode != "multi-agent" && len(normalizeScheduleGroupNames(sched.GroupNames)) == 0 {
			return fmt.Errorf("schedules[%d].group_names is required", i)
		}
		if err := validateScheduleRuntimePolicy(sched); err != nil {
			return fmt.Errorf("schedules[%d]: %w", i, err)
		}
		for _, dependencyID := range scheduleDependencyIDs(sched) {
			if _, ok := scheduleIDs[dependencyID]; !ok {
				return fmt.Errorf("schedules[%d] references unknown dependency schedule %q", i, dependencyID)
			}
		}
	}
	// Dependencies form a directed graph. Reject cycles at authoring time so a
	// pair (or longer chain) of queue_latest schedules cannot wait forever.
	dependencies := make(map[string][]string, len(m.Schedules))
	for _, sched := range m.Schedules {
		if dependencyIDs := scheduleDependencyIDs(sched); len(dependencyIDs) > 0 {
			dependencies[sched.ID] = dependencyIDs
		}
	}
	visitState := make(map[string]uint8, len(dependencies))
	var visit func(string) error
	visit = func(scheduleID string) error {
		switch visitState[scheduleID] {
		case 1:
			return fmt.Errorf("schedule dependency cycle includes %q", scheduleID)
		case 2:
			return nil
		}
		visitState[scheduleID] = 1
		for _, dependencyID := range dependencies[scheduleID] {
			if err := visit(dependencyID); err != nil {
				return err
			}
		}
		visitState[scheduleID] = 2
		return nil
	}
	for scheduleID := range dependencies {
		if err := visit(scheduleID); err != nil {
			return err
		}
	}

	return nil
}

func validWorkflowFolderAlias(alias string) bool {
	if alias == "" || len(alias) > 64 {
		return false
	}
	for i, r := range alias {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || (i > 0 && (r == '-' || r == '_')) {
			continue
		}
		return false
	}
	return true
}

func workflowFolderAliasEnvKey(alias string) string {
	var builder strings.Builder
	for _, r := range strings.ToUpper(strings.TrimSpace(alias)) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			builder.WriteRune(r)
		} else {
			builder.WriteByte('_')
		}
	}
	return builder.String()
}

func normalizeScheduleGroupNames(groupNames []string) []string {
	seen := make(map[string]struct{}, len(groupNames))
	normalized := make([]string, 0, len(groupNames))
	for _, groupName := range groupNames {
		trimmed := strings.TrimSpace(groupName)
		if trimmed == "" {
			continue
		}
		if _, exists := seen[trimmed]; exists {
			continue
		}
		seen[trimmed] = struct{}{}
		normalized = append(normalized, trimmed)
	}
	return normalized
}

func validateScheduleGroupNamesForWorkspace(ctx context.Context, workspacePath string, groupNames []string) ([]string, error) {
	normalized := normalizeScheduleGroupNames(groupNames)
	if len(normalized) == 0 {
		return nil, fmt.Errorf("group_names is required and must contain at least one group name")
	}

	content, exists, err := readFileFromWorkspace(ctx, workspacePath+"/variables/variables.json")
	if err != nil {
		return nil, fmt.Errorf("failed to read variables.json: %w", err)
	}
	if !exists {
		return nil, fmt.Errorf("variables/variables.json not found for %s", workspacePath)
	}

	var manifest VariablesManifest
	if err := json.Unmarshal([]byte(content), &manifest); err != nil {
		return nil, fmt.Errorf("failed to parse variables.json: %w", err)
	}
	if len(manifest.Groups) == 0 {
		return nil, fmt.Errorf("workflow has no variable groups; schedules must specify at least one valid group name")
	}

	validGroups := make(map[string]struct{}, len(manifest.Groups))
	available := make([]string, 0, len(manifest.Groups))
	for _, group := range manifest.Groups {
		groupName := strings.TrimSpace(group.Name)
		if groupName == "" {
			continue
		}
		if _, exists := validGroups[groupName]; exists {
			continue
		}
		validGroups[groupName] = struct{}{}
		available = append(available, groupName)
	}
	sort.Strings(available)

	for _, groupName := range normalized {
		if _, ok := validGroups[groupName]; !ok {
			return nil, fmt.Errorf("unknown group name %q; available groups: %s", groupName, strings.Join(available, ", "))
		}
	}

	return normalized, nil
}

// --- Default factory ---

// NewWorkflowManifest creates a manifest with defaults.
func NewWorkflowManifest(label string) *WorkflowManifest {
	now := time.Now().UTC().Format(time.RFC3339)
	noGlobalSecrets := []string{}
	return &WorkflowManifest{
		CodeLayoutVersion: 1,
		SchemaVersion:     WorkflowManifestSchemaVersion,
		ID:                "wf_" + uuid.New().String()[:8],
		Version:           WorkflowContractCurrentVersion,
		Label:             label,
		Capabilities: WorkflowCapabilities{
			SelectedServers:           []string{},
			SelectedTools:             []string{},
			SelectedSkills:            []string{},
			SelectedSecrets:           []string{},
			SelectedGlobalSecretNames: &noGlobalSecrets,
			BrowserMode:               browser.DefaultMode(),
		},
		Schedules: []WorkflowSchedule{},
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// --- Workspace I/O ---

// manifestPath returns the workspace-relative path to workflow.json for a given workspace.
func manifestPath(workspacePath string) string {
	return workspacePath + "/workflow.json"
}

// ReadWorkflowManifest reads and parses workflow.json from a workspace.
// Returns (manifest, true, nil) if found, (nil, false, nil) if not found, (nil, false, error) on error.
func ReadWorkflowManifest(ctx context.Context, workspacePath string) (*WorkflowManifest, bool, error) {
	return readWorkflowManifest(ctx, workspacePath, true)
}

// readWorkflowManifest can inspect existing settings without persisting read-time migrations.
func readWorkflowManifest(ctx context.Context, workspacePath string, persistMigrations bool) (*WorkflowManifest, bool, error) {
	content, exists, err := readFileFromWorkspace(ctx, manifestPath(workspacePath))
	if err != nil {
		return nil, false, fmt.Errorf("failed to read workflow.json: %w", err)
	}
	if !exists {
		return nil, false, nil
	}

	if !isProductRuntimeManifestWorkspace(workspacePath) && !strings.Contains(filepath.ToSlash(workspacePath), "/.relay_releases/") {

	}
	var m WorkflowManifest
	if err := json.Unmarshal([]byte(content), &m); err != nil {
		// Resilience: the backup/publish blocks are authored by the builder agent,
		// so a shape it chose (e.g. a richer `targets`) must NEVER make the whole
		// manifest unparseable and hide the workflow from the UI. Retry with those
		// optional blocks dropped so the workflow still loads; only a genuinely
		// broken core manifest is a hard error.
		stripped, droppedKeys := stripOptionalConfigBlocks([]byte(content))
		if len(droppedKeys) > 0 {
			if err2 := json.Unmarshal(stripped, &m); err2 == nil {
				log.Printf("[MANIFEST] %s: dropped malformed config block(s) %v so the workflow still loads (parse error: %v)", workspacePath, droppedKeys, err)
				m.MalformedConfig = droppedKeys
			} else {
				return nil, false, fmt.Errorf("failed to parse workflow.json: %w", err)
			}
		} else {
			return nil, false, fmt.Errorf("failed to parse workflow.json: %w", err)
		}
	}

	if err := loadWorkflowEntryActions(ctx, workspacePath, &m); err != nil {
		return nil, false, err
	}
	if err := loadWorkflowTimerActions(ctx, workspacePath, &m); err != nil {
		return nil, false, err
	}

	// Track whether any schedule IDs need auto-assignment before applying defaults.
	hadMissingLabel := strings.TrimSpace(m.Label) == ""
	if hadMissingLabel {
		m.Label = workflowLabelFromWorkspacePath(workspacePath)
	}
	hadEmptyScheduleID := false
	for _, s := range m.Schedules {
		if s.ID == "" {
			hadEmptyScheduleID = true
			break
		}
	}

	// Apply defaults for missing fields from older schema versions
	applyManifestDefaults(&m)
	// Retired Flow timers never execute. Ignore them on read so an older
	// draft or frozen release remains callable; the next draft save removes
	// them through the normal manifest writer and changelog. Never rewrite a
	// published snapshot for this product policy change.
	if m.Kind == "relay" {
		retired := map[string]bool{}
		kept := make([]WorkflowSchedule, 0, len(m.Schedules))
		for _, schedule := range m.Schedules {
			kind := scheduleTypeOrDefault(schedule.ScheduleType)
			if kind == "cron" || kind == "calendar" {
				retired[schedule.ID] = true
				continue
			}
			kept = append(kept, schedule)
		}
		if len(retired) > 0 {
			for i := range kept {
				deps := []string{}
				for _, id := range scheduleDependencyIDs(kept[i]) {
					if !retired[id] {
						deps = append(deps, id)
					}
				}
				kept[i].AfterScheduleID = ""
				kept[i].AfterScheduleIDs = deps
			}
		}
		m.Schedules = kept
	}
	llmConfigMigrated := workflowtypes.NormalizePresetLLMConfig(m.Capabilities.LLMConfig)

	// A field retired from the Go schema (e.g. a past execution_defaults knob
	// that no longer does anything) otherwise lingers in workflow.json forever:
	// json.Unmarshal above already silently dropped it from m, but nothing ever
	// rewrites the file to match. Detect that drift on every open/run so stale
	// prose can't sit there implying behavior it no longer has.
	staleTopLevel, staleCapabilities := staleManifestFields([]byte(content))
	hasStaleFields := len(staleTopLevel) > 0 || len(staleCapabilities) > 0

	// Persist auto-assigned schedule IDs (and any stale-field prune) so
	// subsequent lookups see the cleaned-up manifest. Skip the write-back when
	// we had to drop a malformed config block on read — rewriting now would
	// silently erase the user's backup/publish config from disk.
	// Crew deliberately shares the AgentWorks runtime reader for capabilities,
	// but its workflow.json is owned by the Work product schema. Rewriting that
	// file through WorkflowManifest drops Crew-only fields such as identity and
	// authenticated triggers (and can narrow its schedule records). Keep this
	// path read-only; Crew's product services own all of its migrations/writes.
	// A Code project's workflow.json is the same product-owned file (PLAT-701):
	// its schedules carry no group_names, so the rewrite failed validation on
	// every read, and had it passed it would have dropped the product fields.
	heldRoot, _ := ctx.Value(teamLockHeldKey{}).(string)
	mayPersistManifestMigrations := persistMigrations && heldRoot != workspacePath && !isProductRuntimeManifestWorkspace(workspacePath) && !strings.Contains(filepath.ToSlash(workspacePath), "/.relay_releases/")
	if mayPersistManifestMigrations && (hadMissingLabel || hadEmptyScheduleID || llmConfigMigrated || hasStaleFields) && len(m.MalformedConfig) == 0 {
		if hasStaleFields {
			log.Printf("[MANIFEST] %s: pruning retired field(s) no longer in schema — top-level=%v capabilities=%v",
				workspacePath, staleTopLevel, staleCapabilities)
		}
		if hasStaleFields && !claimStaleManifestPrune(workspacePath) {
			// Pruned (or tried) a moment ago and the fields are still there: do not write on every read.
			return &m, true, nil
		}
		if err := WriteWorkflowManifest(ctx, workspacePath, &m); err != nil {
			log.Printf("[WARN] ReadWorkflowManifest: failed to persist manifest migrations for %s: %v", workspacePath, err)
		}
	}
	return &m, true, nil
}

// manifestContentUnchanged says whether two workflow.json documents are the same apart from the updated_at stamp.
func manifestContentUnchanged(previous, next []byte) bool {
	var a, b map[string]any
	if json.Unmarshal(previous, &a) != nil || json.Unmarshal(next, &b) != nil {
		return false
	}
	delete(a, "updated_at")
	delete(b, "updated_at")
	return reflect.DeepEqual(a, b)
}

// retiredManifestTopLevelKeys are the top-level workflow.json keys the product removed on purpose. ONLY these are pruned from
// a file: any other key the schema does not model (a Crew's triggers, a field written by another subsystem or by a newer or
// older app version) is kept by the writer (keepUnmodelledManifestFields) and is never treated as stale. Before 2026-10-16 the
// prune detected every unknown key but the writer kept them all, so a workflow with a retired "objective" was rewritten on
// every read (about 270 times a minute) and nothing was ever removed. Add a key here when the product retires one.
var retiredManifestTopLevelKeys = map[string]bool{
	"after_manual_run": true, "execution_defaults": true, "run_retention_count": true,
	"objective": true, "success_criteria": true,
	"oversight_mode": true, "knowledgebase_migration": true, "contract_upgrade_history": true,
}

var (
	staleManifestPruneMu sync.Mutex
	staleManifestPruneAt = map[string]time.Time{}
)

// claimStaleManifestPrune lets one prune per workspace through every minute. A prune normally clears the stale fields
// once and for all; if something writes them back (an older client, a sync), the file is fixed again at most once a
// minute instead of on every read.
func claimStaleManifestPrune(workspacePath string) bool {
	now := time.Now()
	staleManifestPruneMu.Lock()
	defer staleManifestPruneMu.Unlock()
	if last, ok := staleManifestPruneAt[workspacePath]; ok && now.Sub(last) < time.Minute {
		return false
	}
	staleManifestPruneAt[workspacePath] = now
	return true
}

func isProductRuntimeManifestWorkspace(workspacePath string) bool {
	if _, _, crew := workspaceref.MustParse(workspacePath).AnyCrewProject(); crew {
		return true
	}
	return isProjectWorkspacePath(workspacePath)
}

func workflowLabelFromWorkspacePath(workspacePath string) string {
	normalized := strings.Trim(strings.ReplaceAll(strings.TrimSpace(workspacePath), "\\", "/"), "/")
	if normalized == "" {
		return "Workflow"
	}
	if separator := strings.LastIndex(normalized, "/"); separator >= 0 {
		normalized = normalized[separator+1:]
	}
	if normalized == "" {
		return "Workflow"
	}
	return normalized
}

// staleManifestFields reports JSON object keys present in raw workflow.json
// content but absent from the current Go schema, scoped to the top-level
// manifest and its capabilities object, where retired per-field knobs have historically
// accumulated. backup, publish, pulse, and schedules are deliberately NOT
// inspected here: those are agent-authored blocks that intentionally allow
// shapes the Go structs don't fully model (see stripOptionalConfigBlocks),
// so treating their contents as "stale" would risk deleting live data.
func staleManifestFields(content []byte) (topLevel, capabilities []string) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(content, &top); err != nil {
		return nil, nil
	}
	known := knownJSONFieldNames(reflect.TypeOf(WorkflowManifest{}))
	for _, key := range unknownManifestKeys(top, known) {
		if retiredManifestTopLevelKeys[key] {
			topLevel = append(topLevel, key)
		}
	}
	if raw, ok := top["capabilities"]; ok {
		var obj map[string]json.RawMessage
		if json.Unmarshal(raw, &obj) == nil {
			capabilities = unknownManifestKeys(obj, knownJSONFieldNames(reflect.TypeOf(WorkflowCapabilities{})))
		}
	}
	return topLevel, capabilities
}

// knownJSONFieldNames returns the set of JSON key names a struct type declares
// via its `json` tags (ignoring "-" and any ",omitempty"/",string" options).
func knownJSONFieldNames(t reflect.Type) map[string]bool {
	names := make(map[string]bool, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		tag := t.Field(i).Tag.Get("json")
		if tag == "" {
			continue
		}
		name := strings.Split(tag, ",")[0]
		if name == "" || name == "-" {
			continue
		}
		names[name] = true
	}
	return names
}

// unknownManifestKeys returns the keys of obj that aren't in known, sorted for
// stable log output.
func unknownManifestKeys(obj map[string]json.RawMessage, known map[string]bool) []string {
	var extra []string
	for key := range obj {
		if !known[key] {
			extra = append(extra, key)
		}
	}
	sort.Strings(extra)
	return extra
}

// stripOptionalConfigBlocks removes the agent-authored optional config blocks
// ("backup", "publish") from raw workflow.json so a malformed one of them can't
// fail the whole manifest parse. Returns the stripped bytes and the keys removed.
// If the top-level JSON itself can't be parsed, returns no dropped keys (the
// caller then surfaces the original, genuine parse error).
func stripOptionalConfigBlocks(content []byte) ([]byte, []string) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(content, &top); err != nil {
		return content, nil
	}
	var dropped []string
	for _, key := range []string{"backup", "publish"} {
		if _, ok := top[key]; ok {
			delete(top, key)
			dropped = append(dropped, key)
		}
	}
	if len(dropped) == 0 {
		return content, nil
	}
	stripped, err := json.Marshal(top)
	if err != nil {
		return content, nil
	}
	return stripped, dropped
}

func workflowManifestChangelogChanges(previous, current string) []step_based_workflow.PlanFieldChange {
	var before, after interface{}
	beforeOK := strings.TrimSpace(previous) != ""
	if beforeOK && json.Unmarshal([]byte(previous), &before) != nil {
		// An old corrupt artifact is still evidence that the whole manifest was
		// replaced, but it cannot support a truthful field-level comparison.
		return []step_based_workflow.PlanFieldChange{manifestChangelogChange("workflow.json", true, true)}
	}
	if err := json.Unmarshal([]byte(current), &after); err != nil {
		// current is produced by json.MarshalIndent above, so this path is only a
		// defensive fallback. Never omit the fact that a write occurred.
		return []step_based_workflow.PlanFieldChange{manifestChangelogChange("workflow.json", beforeOK, true)}
	}

	var changes []step_based_workflow.PlanFieldChange
	collectWorkflowManifestChangelogChanges(&changes, "workflow.json", before, beforeOK, after, true)
	return changes
}

func collectWorkflowManifestChangelogChanges(changes *[]step_based_workflow.PlanFieldChange, path string, before interface{}, beforeOK bool, after interface{}, afterOK bool) {
	if !beforeOK || !afterOK {
		if beforeOK != afterOK {
			*changes = append(*changes, manifestChangelogChange(path, beforeOK, afterOK))
		}
		return
	}

	beforeObject, beforeIsObject := before.(map[string]interface{})
	afterObject, afterIsObject := after.(map[string]interface{})
	if beforeIsObject && afterIsObject {
		keys := make(map[string]struct{}, len(beforeObject)+len(afterObject))
		for key := range beforeObject {
			keys[key] = struct{}{}
		}
		for key := range afterObject {
			keys[key] = struct{}{}
		}
		orderedKeys := make([]string, 0, len(keys))
		for key := range keys {
			orderedKeys = append(orderedKeys, key)
		}
		sort.Strings(orderedKeys)
		for _, key := range orderedKeys {
			beforeValue, beforePresent := beforeObject[key]
			afterValue, afterPresent := afterObject[key]
			collectWorkflowManifestChangelogChanges(changes, path+"."+key, beforeValue, beforePresent, afterValue, afterPresent)
		}
		return
	}

	// Collections are an atomic artifact-level field for changelog purposes.
	// Values are never persisted in this evidence list.
	if !reflect.DeepEqual(before, after) {
		*changes = append(*changes, manifestChangelogChange(path, true, true))
	}
}

func manifestChangelogChange(path string, beforeOK, afterOK bool) step_based_workflow.PlanFieldChange {
	change := step_based_workflow.PlanFieldChange{Field: path}
	switch {
	case !beforeOK:
		change.OldValue = "absent"
		change.NewValue = "added"
	case !afterOK:
		change.OldValue = "removed"
		change.NewValue = "absent"
	default:
		change.OldValue = "present"
		change.NewValue = "changed"
	}
	return change
}

// WriteWorkflowManifest validates and writes workflow.json to a workspace.
func WriteWorkflowManifest(ctx context.Context, workspacePath string, m *WorkflowManifest) error {
	// All team settings writers share the source-edit commit lock. A caller
	// already holding it (team assignment/commit) passes trusted context.
	if held, _ := ctx.Value(teamLockHeldKey{}).(string); held != workspacePath {
		// Read the saved policy without read-time migration: the reader can
		// itself call this writer to persist defaults.
		raw, ok, e := readFileFromWorkspace(ctx, manifestPath(workspacePath))
		if e != nil {
			return e
		}
		var saved WorkflowManifest
		if ok {
			if e := json.Unmarshal([]byte(raw), &saved); e != nil {
				return fmt.Errorf("parse saved workflow policy: %w", e)
			}
		}
		if saved.Team != nil {
			unlock, e := teamLock(ctx, workspacePath)
			if e != nil {
				return e
			}
			defer unlock()
			ctx = context.WithValue(ctx, teamLockHeldKey{}, workspacePath)
		}
	}

	lockPath := filepath.Join(step_based_workflow.GetPromptDocsRoot(), filepath.FromSlash(manifestPath(workspacePath)))
	if _, statErr := os.Stat(lockPath); statErr == nil {
		unlock, lockErr := knowledgeManifestLockWithWait(ctx, lockPath)
		if lockErr != nil {
			return lockErr
		}
		defer unlock()
	}
	previous, previousExists, readErr := readFileFromWorkspace(ctx, manifestPath(workspacePath))
	if readErr != nil {
		return fmt.Errorf("read existing workflow.json before update: %w", readErr)
	}
	// Source layout is a creation-time contract, not an editable capability.
	// Older clients omit this field; never silently switch their source tree.
	if previousExists {
		var prior WorkflowManifest
		if err := json.Unmarshal([]byte(previous), &prior); err != nil {
			return fmt.Errorf("parse existing workflow.json before update: %w", err)
		}
		if err := loadWorkflowEntryActions(ctx, workspacePath, &prior); err != nil {
			return err
		}
		if err := loadWorkflowTimerActions(ctx, workspacePath, &prior); err != nil {
			return err
		}
		if err := validateTeamManifestUpdate(ctx, &prior, m); err != nil {
			return err
		}
		stampTeamScheduleChanges(ctx, &prior, m)
		m.CodeLayoutVersion = prior.CodeLayoutVersion
		m.SharedKnowledgebase = prior.SharedKnowledgebase
		m.KnowledgebaseMode = prior.KnowledgebaseMode
		m.BrainAccess = prior.BrainAccess
		m.KnowledgebaseContractHistory = prior.KnowledgebaseContractHistory
	}
	ctx = context.WithValue(ctx, teamManifestPermitKey{}, true)
	workflowtypes.NormalizePresetLLMConfig(m.Capabilities.LLMConfig)
	// Ensure nil slices become empty arrays in JSON
	ensureManifestSlices(m)

	// Set updated timestamp
	if m.Team != nil {
		m.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	} else {
		m.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	}

	if err := ValidateManifest(m); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidWorkflowManifest, err)
	}

	if err := saveWorkflowTimerActions(ctx, workspacePath, m); err != nil {
		return err
	}
	if err := saveWorkflowEntryActions(ctx, workspacePath, m); err != nil {
		return err
	}
	persisted := *m
	persisted.Schedules = nil
	data, err := json.MarshalIndent(&persisted, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal workflow.json: %w", err)
	}
	// A Crew's workflow.json carries top-level fields this struct does not model (its internal `triggers`, its `identity`).
	// Rewriting it from the struct erased them: Server C's blueprint Crew lost the trigger a workflow step called it by, and every
	// webhook run then died with "internal trigger not found" (2026-10-07). Keep what the file already had that the struct
	// has no field for; fields the struct does model still follow the struct (clearing one still clears it).
	if previousExists {
		data = keepUnmodelledManifestFields([]byte(previous), data)
	}

	// A save that changes nothing but its own updated_at stamp is not a save: skipping it keeps the folder from being marked
	// dirty and the manifest index from re-reading every manifest, so no future write-on-read path can feed itself (the
	// 2026-10-15 prune loop did exactly that).
	if previousExists && manifestContentUnchanged([]byte(previous), data) {
		return nil
	}
	if err := writeFileToWorkspace(ctx, manifestPath(workspacePath), string(data)); err != nil {
		return fmt.Errorf("failed to write workflow.json: %w", err)
	}

	if !previousExists || previous != string(data) {
		if changes := workflowManifestChangelogChanges(previous, string(data)); len(changes) > 0 {
			logCtx := ctx
			if m.Team != nil {
				// This typed save already passed policy validation. Its audit
				// writes must work even after ownership transfers to another user.
				logCtx = context.WithValue(ctx, teamCommitPermitKey{}, true)
			}
			// Every settings save names the person who made it (website,
			// Builder chat or MCP caller, or the owner an automatic job runs as).
			logCtx = withChangelogActor(logCtx)
			step_based_workflow.LogCanonicalArtifactChange(
				logCtx, workspacePath, "write_workflow_manifest",
				"Recorded workflow.json field changes for artifact drift review.",
				changes, workflowManifestChangelogReader, writeFileToWorkspace, createServerLogger(),
				"workflow.json", previous, string(data),
			)
		}
	}
	return nil
}

// workflowManifestChangelogReader adapts the server's three-result read to the
// two-result shape the changelog writer expects. A missing file reads as empty,
// which is how the changelog writer creates its first entry.
func workflowManifestChangelogReader(ctx context.Context, path string) (string, error) {
	content, exists, err := readFileFromWorkspace(ctx, path)
	if err != nil {
		return "", err
	}
	if !exists {
		return "", nil
	}
	return content, nil
}

// --- Internal helpers ---

// applyManifestDefaults fills in defaults for fields that may be missing from older schema versions.
func applyManifestDefaults(m *WorkflowManifest) {

	if m.SchemaVersion == 0 {
		m.SchemaVersion = 1
	}
	if m.Capabilities.BrowserMode == "" {
		m.Capabilities.BrowserMode = browser.DefaultMode()
	}
	if m.Capabilities.SelectedServers == nil {
		m.Capabilities.SelectedServers = []string{}
	}
	if m.Capabilities.SelectedTools == nil {
		m.Capabilities.SelectedTools = []string{}
	}
	if m.Capabilities.SelectedSkills == nil {
		m.Capabilities.SelectedSkills = []string{}
	}
	if m.Capabilities.SelectedSecrets == nil {
		m.Capabilities.SelectedSecrets = []string{}
	}
	if m.Schedules == nil {
		m.Schedules = []WorkflowSchedule{}
	}
	if m.Backup != nil && m.Backup.Mode == "" {
		m.Backup.Mode = "agent"
	}
	// Auto-assign IDs to schedules that pre-date the ID field.
	for i := range m.Schedules {
		if m.Schedules[i].ID == "" {
			m.Schedules[i].ID = uuid.New().String()
		}
	}
}

// ensureManifestSlices ensures all slice fields are non-nil so they serialize as [] not null.
func ensureManifestSlices(m *WorkflowManifest) {
	applyManifestDefaults(m) // reuses the same logic
}

// --- Workspace discovery ---

// DiscoverWorkflowManifests scans all workspace folders to find those with workflow.json.
// It calls the workspace API to list top-level folders, then checks each for a manifest.
func DiscoverWorkflowManifests(ctx context.Context) ([]DiscoveredWorkflow, error) {
	return discoverWorkflowManifests(ctx, true)
}

func discoverWorkflowManifests(ctx context.Context, persistMigrations bool) ([]DiscoveredWorkflow, error) {
	// List all workspace folders via the workspace API
	folders, err := listWorkspaceFolders(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list workspace folders: %w", err)
	}

	var results []DiscoveredWorkflow
	for _, folder := range folders {
		manifest, exists, err := readWorkflowManifest(ctx, folder, persistMigrations)
		if err != nil {
			log.Printf("[WARN] DiscoverWorkflowManifests: error reading manifest from %s: %v", folder, err)
			continue
		}
		if !exists {
			continue
		}

		results = append(results, DiscoveredWorkflow{
			WorkspacePath: folder,
			Manifest:      manifest,
		})
	}

	return results, nil
}

// DiscoveredWorkflow pairs a manifest with its workspace path.
type DiscoveredWorkflow struct {
	WorkspacePath string            `json:"workspace_path"`
	Manifest      *WorkflowManifest `json:"manifest"`
	// MyAccess is the requesting user's level on this workflow (owner, write,
	// read); filled by filterWorkflowManifestsForUser so the UI can gate
	// editing per workflow instead of per account.
	MyAccess WorkflowAccessLevel `json:"my_access,omitempty"`
}

// listWorkspaceFolders returns all top-level folders under the "Workflow" namespace.
// Uses the workspace API's /api/documents?folder=Workflow&max_depth=1 endpoint.
func listWorkspaceFolders(ctx context.Context) ([]string, error) {
	apiURL := getWorkspaceAPIURL() + "/api/documents"
	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	q := req.URL.Query()
	q.Add("folder", "Workflow")
	q.Add("max_depth", "1")
	req.URL.RawQuery = q.Encode()

	resp, err := workspaceHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to call workspace API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return []string{}, nil
	}
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("workspace API returned status %d: %s", resp.StatusCode, string(respBody))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	// Parse workspace API response: { success: true, data: [ { filepath, type, children } ] }
	var apiResp struct {
		Success bool `json:"success"`
		Data    []struct {
			Filepath string `json:"filepath"`
			Type     string `json:"type"`
			Children []struct {
				Filepath string `json:"filepath"`
				Type     string `json:"type"`
			} `json:"children"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, fmt.Errorf("failed to parse folder listing: %w", err)
	}

	var folders []string
	// The root "Workflow" folder is data[0], its children are the workflow workspaces
	for _, root := range apiResp.Data {
		for _, child := range root.Children {
			if child.Type == "folder" && child.Filepath != "" {
				folders = append(folders, child.Filepath)
			}
		}
	}

	return folders, nil
}

// defaultPaceThresholdPercent is where "close to the wall" starts.
//
// Deliberately not near 100: the whole point is to cross a reset BEFORE the
// window is exhausted, and a step can consume several percent on its own, so a
// threshold at 98 would routinely be overtaken mid-step by the very run it is
// meant to protect.
const defaultPaceThresholdPercent = 85

// PaceThreshold returns the configured usage threshold, or the default.
func (m *WorkflowManifest) PaceThreshold() int {
	if m == nil || m.PaceThresholdPercent <= 0 || m.PaceThresholdPercent > 100 {
		return defaultPaceThresholdPercent
	}
	return m.PaceThresholdPercent
}

// PacingEnabled reports whether this workflow opted into quota pacing.
func (m *WorkflowManifest) PacingEnabled() bool {
	return m != nil && m.PaceOnLowQuota
}

// workflowManifestKnownKeys is every top-level JSON key WorkflowManifest models (its `json` tags).
var workflowManifestKnownKeys = func() map[string]bool {
	known := map[string]bool{}
	t := reflect.TypeOf(WorkflowManifest{})
	for i := 0; i < t.NumField(); i++ {
		name := strings.Split(t.Field(i).Tag.Get("json"), ",")[0]
		if name != "" && name != "-" {
			known[name] = true
		}
	}
	return known
}()

// keepUnmodelledManifestFields returns written with every top-level key of previous that WorkflowManifest has no field for.
// Anything it cannot parse is left exactly as written.
func keepUnmodelledManifestFields(previous, written []byte) []byte {
	var before, after map[string]json.RawMessage
	if json.Unmarshal(previous, &before) != nil || json.Unmarshal(written, &after) != nil {
		return written
	}
	var version string
	_ = json.Unmarshal(after["version"], &version)
	kept := false
	for key, value := range before {
		// The one-off skills upgrade uses a raw receipt until its stamp succeeds.
		if key == "workflow_skills_migration" && step_based_workflow.UsesWorkflowSkills(version) {
			continue
		}
		if workflowManifestKnownKeys[key] || retiredManifestTopLevelKeys[key] {
			continue
		}
		if _, exists := after[key]; !exists {
			after[key] = value
			kept = true
		}
	}
	if !kept {
		return written
	}
	merged, err := json.MarshalIndent(after, "", "  ")
	if err != nil {
		return written
	}
	return merged
}

// RuntimeServers is the app list the agent runtime takes for these capabilities (see runtimeAppSelection).
func (c WorkflowCapabilities) RuntimeServers() []string {
	return runtimeAppSelection(c.SelectedServers, c.ChosenVaultAppsOnly)
}

// runtimeAppSelection turns a saved app list and its "Use all my apps automatically" setting into the agent
// runtime's list. With every Vault app on, hand-picked Vault apps are dropped because the runtime adds them all; with
// chosen apps only, the agent library's internal no-automatic-apps sentinel is added. People and saved settings never
// carry the sentinel.
func runtimeAppSelection(servers []string, chosenOnly bool) []string {
	out := []string{}
	for _, name := range servers {
		name = strings.TrimSpace(name)
		if name == "" || name == mcpclient.NoServers || (!chosenOnly && strings.HasPrefix(name, "vault_")) {
			continue
		}
		out = append(out, name)
	}
	if chosenOnly {
		out = append(out, mcpclient.NoServers)
	}
	return out
}

// savedAppSelection removes the runtime sentinel from a list about to be saved.
func savedAppSelection(servers []string) []string {
	out := []string{}
	for _, name := range servers {
		if name = strings.TrimSpace(name); name != "" && name != mcpclient.NoServers {
			out = append(out, name)
		}
	}
	return out
}
