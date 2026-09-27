package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	step_based_workflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/pulsemodules"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/schedulepolicy"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"

	"github.com/google/uuid"
	"github.com/robfig/cron/v3"
)

// Current manifest schema version. This is the JSON shape version.
const WorkflowManifestSchemaVersion = 1

// WorkflowContractCurrentVersion is the product-managed workflow behavior
// contract version. Unlike schema_version, this gates agent-run workflow
// upgrades. An operator starts pending migrations from the workflow chat, and
// the version is stamped only after the workflow has been checked or migrated.
const WorkflowContractCurrentVersion = workflowContractNestedAgentArtifactsVersion

const workflowContractExplicitSchedulePulseVersion = schedulepolicy.ExplicitPulseContractVersion

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

const workflowContractInitialVersion = "1.0.0"
const workflowContractMessageSequenceCodeVersion = "1.0.10"
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
const workflowContractScriptedTypeStaysRegularVersion = "1.0.37"

// workflowContractDeclaredExecutionModeRetiredVersion (PLAT-287, half 1):
// every step's plan type states its execution model explicitly -- legacy
// agentic regular steps become the message_sequence they already ran as,
// declared-scripted sequences become regular -- so that after this version
// every regular step is a declared scripted one. declared_execution_mode
// itself is NOT touched here: the runtime still reads it. A later version
// flips the runtime to "regular means scripted", stops every writer, and
// strips and deletes the field, gated on no legacy agentic regular step
// remaining on any live workflow.
const workflowContractDeclaredExecutionModeRetiredVersion = "1.0.38"

// workflowContractDeclaredExecutionModeStrippedVersion (PLAT-287, half 2):
// the runtime now decides a step's execution model from its plan type alone
// (regular = scripted, message_sequence = conversational),
// so the retired declared_execution_mode / _reason keys are stripped from
// planning/step_config.json by
// strip_declared_execution_mode, which refuses while any regular step still
// carries declared_execution_mode="agentic" (i.e. v1.0.38 did not complete).
const workflowContractDeclaredExecutionModeStrippedVersion = "1.0.39"

const (
	DefaultRunRetentionCount = 10
	MaxRunRetentionCount     = 50
)

// WorkflowManifest is the top-level workflow.json structure that lives in each workspace.
type WorkflowManifest struct {
	KnowledgebaseSources []workflowtypes.KnowledgebaseSource `json:"knowledgebase_sources,omitempty"`
	CodeLayoutVersion    int                                 `json:"code_layout_version,omitempty"` // 0: legacy learnings; 1: persistent code tree
	SchemaVersion        int                                 `json:"schema_version"`
	ID                   string                              `json:"id"`
	// Kind distinguishes a Relay from a general workflow while reusing the
	// same manifest, schedules, access rules, runner, and run history.
	Kind                   string                                `json:"kind,omitempty"`
	RelayOutputStepID      string                                `json:"relay_output_step_id,omitempty"`
	Version                string                                `json:"version,omitempty"`
	ContractUpgradeHistory []WorkflowContractUpgradeHistoryEntry `json:"contract_upgrade_history,omitempty"`
	Label                  string                                `json:"label"`
	Icon                   string                                `json:"icon,omitempty"`
	Capabilities           WorkflowCapabilities                  `json:"capabilities"`
	ExecutionDefs          WorkflowExecutionDefaults             `json:"execution_defaults"`
	Schedules              []WorkflowSchedule                    `json:"schedules"`
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
	RunRetentionCount    *int                                        `json:"run_retention_count,omitempty"`
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

	// Auto-improvement framework fields. See docs/workflow/auto_improvement_framework.md.
	//
	// Only fields that drive HARD behavioral gates live here. Workflow profile
	// (deterministic / exploratory / contextual classification, plan-stability
	// guidance, dual-mode declarations) belongs in semantic workflow artifacts
	// and typed Pulse records; prose captures nuance that enums cannot.
	OversightMode OversightMode `json:"oversight_mode,omitempty"`

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

	// Pulse contains owner-approved workflow-specific review lenses. These
	// specialize the stable reviewer contracts; they never replace them.
	Pulse *WorkflowPulseConfig `json:"pulse,omitempty"`

	// Backup is declarative configuration for builder-agent managed backup.
	// Operational status is written separately to backup/status.json so normal
	// backup attempts do not churn workflow.json.
	Backup *WorkflowBackupConfig `json:"backup,omitempty"`

	// Publish is declarative config for builder-agent managed publishing of the
	// workflow's HTML artifacts (Pulse log, report dashboard) to a public URL.
	// Operational status (incl. the URL) is written to publish/status.json so
	// publish attempts do not churn workflow.json.
	Publish *WorkflowPublishConfig `json:"publish,omitempty"`

	// MalformedConfig lists optional config blocks (e.g. "backup", "publish") that
	// failed to parse and were dropped so the workflow could still load. Transient
	// (never serialized): set during ReadWorkflowManifest, used to avoid clobbering
	// the on-disk config on write-back and to flag the issue.
	MalformedConfig []string `json:"-"`
}

type WorkflowContractUpgradeHistoryEntry struct {
	Version   string `json:"version"`
	AppliedAt string `json:"applied_at"`
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

type WorkflowPulseConfig struct {
	// Enabled turns on the workflow's own Pulse schedule (see Schedule). Normal
	// schedules only run basic stewardship (backup, publish, notify) after a run;
	// the full Pulse review never runs inline after a normal run.
	Enabled bool `json:"enabled,omitempty"`
	// Schedule controls when the full Pulse runs. Nil means the defaults of
	// EffectivePulseSchedule: self-deciding, at most daily, at least weekly.
	Schedule *WorkflowPulseSchedule `json:"schedule,omitempty"`
	// FocusAreas are the user's current priorities for Goal Work, e.g. "find
	// more audience strategies like SaaS Builder". Goal Work starts there each
	// pass; they direct attention, they do not limit what it may consider.
	FocusAreas []string `json:"focus_areas,omitempty"`
	// Autonomy holds Goal Work's permission levels. Nil means the defaults:
	// Run is auto (Goal Work may run existing steps itself); Outward and Change
	// ask the user.
	Autonomy              *WorkflowPulseAutonomy         `json:"autonomy,omitempty"`
	AdvisorSpecialization *WorkflowAdvisorSpecialization `json:"advisor_specialization,omitempty"`
	// DisabledReviewModules is the owner-controlled denylist for optional Pulse
	// reviewers. Gate still records these modules in each worklist for an
	// auditable skip, but the scheduler never launches their review agents.
	DisabledReviewModules []string `json:"disabled_review_modules,omitempty"`
}

const (
	pulseScheduleModeSelf  = "self"
	pulseScheduleModeFixed = "fixed"

	defaultPulseMinIntervalHours = 6
	defaultPulseMaxIntervalHours = 168
	// defaultPulseFirstRunHour is the local hour of a workflow's first
	// self-scheduled Pulse when it has not yet chosen a time itself.
	defaultPulseFirstRunHour = 6
)

// WorkflowPulseSchedule is the workflow's own Pulse schedule. In "self" mode
// each Pulse pass chooses its next run (record_pulse_next_run) and the platform
// only enforces the min/max interval guards. "fixed" pins a cron instead.
type WorkflowPulseSchedule struct {
	Mode             string `json:"mode,omitempty"`
	Cron             string `json:"cron,omitempty"`
	Timezone         string `json:"timezone,omitempty"`
	MinIntervalHours int    `json:"min_interval_hours,omitempty"`
	MaxIntervalHours int    `json:"max_interval_hours,omitempty"`
}

// EffectivePulseSchedule fills defaults. The timezone falls back to the most
// common timezone of the workflow's enabled schedules, then the server's.
func (m *WorkflowManifest) EffectivePulseSchedule() WorkflowPulseSchedule {
	schedule := WorkflowPulseSchedule{}
	if m != nil && m.Pulse != nil && m.Pulse.Schedule != nil {
		schedule = *m.Pulse.Schedule
	}
	schedule.Mode = strings.ToLower(strings.TrimSpace(schedule.Mode))
	if schedule.Mode != pulseScheduleModeFixed {
		schedule.Mode = pulseScheduleModeSelf
	}
	if schedule.MinIntervalHours <= 0 {
		schedule.MinIntervalHours = defaultPulseMinIntervalHours
	}
	if schedule.MaxIntervalHours <= 0 {
		schedule.MaxIntervalHours = defaultPulseMaxIntervalHours
	}
	if schedule.MaxIntervalHours < schedule.MinIntervalHours {
		schedule.MaxIntervalHours = schedule.MinIntervalHours
	}
	if strings.TrimSpace(schedule.Timezone) == "" && m != nil {
		counts := map[string]int{}
		best := ""
		for _, s := range m.Schedules {
			tz := strings.TrimSpace(s.Timezone)
			if !s.Enabled || tz == "" {
				continue
			}
			counts[tz]++
			if best == "" || counts[tz] > counts[best] {
				best = tz
			}
		}
		schedule.Timezone = best
	}
	return schedule
}

// WorkflowPulseAutonomy is Goal Work's per-workflow permission setting.
// Preparing work is always on. Each level is "auto" (Goal Work does it
// itself) or "ask" (it prepares the work and creates a decision).
type WorkflowPulseAutonomy struct {
	// Run: run existing workflow steps and routes. Default auto.
	Run string `json:"run,omitempty"`
	// Outward: post, send or contact anyone beyond what existing steps
	// normally do. Default ask.
	Outward string `json:"outward,omitempty"`
	// Change: edit the plan, step settings and schedules. soul.md goals and
	// constraints always go to the user. Default ask.
	Change string `json:"change,omitempty"`
}

// PulseAutonomyLevels is the resolved setting with defaults applied.
type PulseAutonomyLevels struct {
	Run     string `json:"run"`
	Outward string `json:"outward"`
	Change  string `json:"change"`
}

const (
	maxPulseFocusAreas      = 10
	maxPulseFocusAreaLength = 300
)

// normalizePulseFocusAreas trims, drops blanks and duplicates, and enforces the
// small limits that keep them a priority list rather than a second prompt.
func normalizePulseFocusAreas(values []string) ([]string, error) {
	out := []string{}
	seen := map[string]bool{}
	for _, raw := range values {
		value := strings.Join(strings.Fields(raw), " ")
		if value == "" || seen[strings.ToLower(value)] {
			continue
		}
		if len(value) > maxPulseFocusAreaLength {
			return nil, fmt.Errorf("pulse.focus_areas entries must be at most %d characters", maxPulseFocusAreaLength)
		}
		seen[strings.ToLower(value)] = true
		out = append(out, value)
	}
	if len(out) > maxPulseFocusAreas {
		return nil, fmt.Errorf("pulse.focus_areas allows at most %d entries", maxPulseFocusAreas)
	}
	return out, nil
}

func normalizePulseAutonomyRun(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "auto":
		return "auto", nil
	case "ask":
		return "ask", nil
	}
	return "", fmt.Errorf("pulse.autonomy.run must be auto or ask")
}

// normalizePulseAutonomyAskDefault normalizes a level whose default is ask.
func normalizePulseAutonomyAskDefault(field, value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "ask":
		return "ask", nil
	case "auto":
		return "auto", nil
	}
	return "", fmt.Errorf("pulse.autonomy.%s must be auto or ask", field)
}

// resolvePulseAutonomy applies the defaults and rejects unknown values.
func resolvePulseAutonomy(a *WorkflowPulseAutonomy) (PulseAutonomyLevels, error) {
	if a == nil {
		return PulseAutonomyLevels{Run: "auto", Outward: "ask", Change: "ask"}, nil
	}
	run, err := normalizePulseAutonomyRun(a.Run)
	if err != nil {
		return PulseAutonomyLevels{}, err
	}
	outward, err := normalizePulseAutonomyAskDefault("outward", a.Outward)
	if err != nil {
		return PulseAutonomyLevels{}, err
	}
	change, err := normalizePulseAutonomyAskDefault("change", a.Change)
	if err != nil {
		return PulseAutonomyLevels{}, err
	}
	return PulseAutonomyLevels{Run: run, Outward: outward, Change: change}, nil
}

func validateWorkflowPulseSchedule(schedule *WorkflowPulseSchedule) error {
	if schedule == nil {
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(schedule.Mode)) {
	case "", pulseScheduleModeSelf:
	case pulseScheduleModeFixed:
		parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
		if _, err := parser.Parse(strings.TrimSpace(schedule.Cron)); err != nil {
			return fmt.Errorf("pulse.schedule.cron is invalid for fixed mode: %w", err)
		}
	default:
		return fmt.Errorf("pulse.schedule.mode must be self or fixed")
	}
	if tz := strings.TrimSpace(schedule.Timezone); tz != "" {
		if _, err := time.LoadLocation(tz); err != nil {
			return fmt.Errorf("pulse.schedule.timezone %q is invalid: %w", tz, err)
		}
	}
	if schedule.MinIntervalHours < 0 || schedule.MaxIntervalHours < 0 {
		return fmt.Errorf("pulse.schedule intervals must not be negative")
	}
	return nil
}

var configurablePulseReviewModules = map[string]bool{
	pulsemodules.TechnicalReviewID:    true,
	pulsemodules.ArchitectureReviewID: true,
	pulsemodules.StrategicReviewID:    true,
}

func normalizeDisabledPulseReviewModules(modules []string) ([]string, error) {
	seen := map[string]bool{}
	result := make([]string, 0, len(modules))
	for _, raw := range modules {
		module := strings.ToLower(strings.TrimSpace(raw))
		if !configurablePulseReviewModules[module] {
			return nil, fmt.Errorf("pulse.disabled_review_modules contains unsupported reviewer %q", raw)
		}
		if seen[module] {
			continue
		}
		seen[module] = true
		result = append(result, module)
	}
	return result, nil
}

func (m *WorkflowManifest) PulseReviewModuleDisabled(module string) bool {
	if m == nil || m.Pulse == nil {
		return false
	}
	module = strings.ToLower(strings.TrimSpace(module))
	for _, disabled := range m.Pulse.DisabledReviewModules {
		if strings.ToLower(strings.TrimSpace(disabled)) == module {
			return true
		}
	}
	return false
}

type WorkflowAdvisorSpecialization struct {
	Version         int    `json:"version"`
	StrategyAuditor string `json:"strategy_auditor"`
	GoalAdvisor     string `json:"goal_advisor"`
	ApprovedInputID string `json:"approved_input_id,omitempty"`
	UpdatedAt       string `json:"updated_at,omitempty"`
}

// HasEnabledPulseReviewSchedule recognizes the retired dedicated-schedule
// representation long enough to migrate existing workflow manifests.
func (m *WorkflowManifest) HasEnabledPulseReviewSchedule() bool {
	if m == nil {
		return false
	}
	for _, schedule := range m.Schedules {
		if schedule.Enabled && schedule.PulseReviewOnly {
			return true
		}
	}
	return false
}

// PulseEnabled reports whether post-run Pulse is enabled. Older manifests used
// an enabled pulse_review_only schedule as the toggle; retain that as a read
// compatibility signal while the next UI save removes the obsolete schedule.
func (m *WorkflowManifest) PulseEnabled() bool {
	if m == nil {
		return false
	}
	if m.Kind == "relay" {
		return false
	}
	if m.Pulse != nil && m.Pulse.Enabled {
		return true
	}
	return m.HasEnabledPulseReviewSchedule() || m.hasLegacyFullPulseSchedule()
}

// hasLegacyFullPulseSchedule keeps Pulse on for workflows that opted into
// review only through a schedule's legacy pulse_mode=full. Normal schedules
// now run basic stewardship; the review moved to the workflow's own Pulse
// schedule, which must not silently turn off for them.
func (m *WorkflowManifest) hasLegacyFullPulseSchedule() bool {
	if m == nil {
		return false
	}
	for _, schedule := range m.Schedules {
		if schedule.Enabled && strings.EqualFold(strings.TrimSpace(schedule.PulseMode), schedulepolicy.LegacyFullPulseMode) {
			return true
		}
	}
	return false
}

const (
	schedulePulseModeOff   = "off"
	schedulePulseModeBasic = "basic"
	schedulePulseModeFull  = "full"
)

// EffectivePulseMode returns this schedule's post-run stewardship policy.
// An omitted pulse_mode intentionally preserves the workflow-wide setting so
// existing manifests retain their behavior: enabled means full Pulse, disabled
// means no post-run Pulse work. Explicit schedule values override that default.
func (m *WorkflowManifest) EffectivePulseMode(schedule WorkflowSchedule) string {
	if m != nil && m.Kind == "relay" {
		return schedulePulseModeOff
	}
	// A normal schedule never runs the full Pulse review; that runs on the
	// workflow's own Pulse schedule (pulse_schedule.go). Legacy "full" and the
	// legacy empty-with-Pulse-enabled default both mean basic stewardship.
	switch schedulepolicy.NormalizePulse(schedule.PulseMode) {
	case schedulePulseModeOff:
		return schedulePulseModeOff
	case schedulePulseModeBasic:
		return schedulePulseModeBasic
	}
	if m.PulseEnabled() {
		return schedulePulseModeBasic
	}
	return schedulePulseModeOff
}

// MigrateLegacyPulseSchedule folds the retired dedicated Pulse schedule into
// pulse.enabled and removes it from the normal schedule list.
func (m *WorkflowManifest) MigrateLegacyPulseSchedule() bool {
	if m == nil {
		return false
	}
	found := false
	enabled := false
	schedules := make([]WorkflowSchedule, 0, len(m.Schedules))
	for _, schedule := range m.Schedules {
		if schedule.PulseReviewOnly {
			found = true
			enabled = enabled || schedule.Enabled
			continue
		}
		schedules = append(schedules, schedule)
	}
	if !found {
		return false
	}
	if m.Pulse == nil {
		m.Pulse = &WorkflowPulseConfig{}
	}
	m.Pulse.Enabled = m.Pulse.Enabled || enabled
	m.Schedules = schedules
	return true
}

type WorkflowBackupConfig struct {
	Enabled      bool                        `json:"enabled"`
	Mode         string                      `json:"mode,omitempty"` // "agent" (default)
	Triggers     WorkflowBackupTriggers      `json:"triggers,omitempty"`
	Destinations []WorkflowBackupDestination `json:"destinations,omitempty"`
	Notes        string                      `json:"notes,omitempty"`
}

type WorkflowBackupTriggers struct {
	AfterScheduledRun bool `json:"after_scheduled_run,omitempty"`
	AfterManualRun    bool `json:"after_manual_run,omitempty"`
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
	Targets       []json.RawMessage            `json:"targets,omitempty"`        // strings ("pulse"/"report") or objects — agent's choice
	DashboardMode string                       `json:"dashboard_mode,omitempty"` // "snapshot" (static HTML)
	URL           string                       `json:"url,omitempty"`            // last published URL (agent-written, mirror of status)
	Triggers      WorkflowBackupTriggers       `json:"triggers,omitempty"`
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
	SelectedServers           []string                       `json:"selected_servers"`
	SelectedTools             []string                       `json:"selected_tools"`
	SelectedSkills            []string                       `json:"selected_skills"`
	SelectedSecrets           []string                       `json:"selected_secrets"`
	SelectedGlobalSecretNames *[]string                      `json:"selected_global_secret_names"` // nil = all, [] = none
	BrowserMode               string                         `json:"browser_mode"`
	CDPPorts                  []int                          `json:"cdp_ports,omitempty"`
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
	// Run-mode chats in agent_tools mode hybrid: the coding CLI's own read,
	// search, skills, todos and subagents; shell and file changes stay on
	// AgentWorks tools. Interactive chats of owners and editors only: step
	// agents, schedules, webhooks, bots and read-only users keep
	// AgentWorks-only tools. On by default: nil means on and only an explicit
	// false turns it off. Read it through NativeAgentToolsEnabled.
	NativeAgentTools *bool `json:"native_agent_tools,omitempty"`
}

// NativeAgentToolsEnabled reports the "Native agent tools" switch, which is on
// unless it was explicitly turned off.
func (c WorkflowCapabilities) NativeAgentToolsEnabled() bool {
	return nativeAgentToolsEnabled(c.NativeAgentTools)
}

// nativeAgentToolsEnabled applies the on-by-default rule shared by workflows
// and crew projects.
func nativeAgentToolsEnabled(setting *bool) bool {
	return setting == nil || *setting
}

// WorkflowNotificationConfig contains only safe references. Credential values
// are kept in the encrypted secret store and resolved immediately before a run.
type WorkflowNotificationConfig struct {
	SlackWebhookSecretName string `json:"slack_webhook_secret_name,omitempty"`

	// Per-summary Slack channels. A Slack Incoming Webhook is bound to ONE
	// channel when it is created and ignores a channel field in the payload, so
	// "send this summary to a different channel" means "post it through a
	// different webhook". Each entry names an encrypted secret holding a
	// complete webhook URL; listing several fans that summary out to several
	// channels. An empty list falls back to SlackWebhookSecretName, so a
	// workflow that never split its channels behaves exactly as before.
	RunSummarySlackWebhookSecretNames   []string `json:"run_summary_slack_webhook_secret_names,omitempty"`
	PulseSummarySlackWebhookSecretNames []string `json:"pulse_summary_slack_webhook_secret_names,omitempty"`

	// RunSummaryInstructions controls the execution/outcome section of a
	// notification. PulseSummaryInstructions controls the review/fix section.
	// Both are ordinary workflow configuration, never secrets, recipients, or
	// delivery credentials.
	RunSummaryInstructions   string   `json:"run_summary_instructions,omitempty"`
	PulseSummaryInstructions string   `json:"pulse_summary_instructions,omitempty"`
	RunSummaryChannels       []string `json:"run_summary_channels,omitempty"`
	PulseSummaryChannels     []string `json:"pulse_summary_channels,omitempty"`

	// Instructions is the legacy shared content preference. Keep it as a
	// fallback so workflows that saved the original single field retain their
	// behavior until the owner saves separate preferences.
	Instructions string `json:"instructions,omitempty"`

	// ExcludeChannels lists account-level delivery channels this workflow opts
	// OUT of, by connector name ("gmail", "slack", "whatsapp"). A channel enabled
	// account-wide is inherited by every workflow; naming it here suppresses it
	// for THIS workflow only, without changing the account-wide configuration.
	ExcludeChannels []string `json:"exclude_channels,omitempty"`

	// GmailConnectionID selects WHICH configured Gmail account sends this
	// workflow's email — the sender, orthogonal to every recipient field here.
	// Empty means "inherit the account-level default connection", so existing
	// workflows are unaffected. An unknown or disabled connection is a
	// configuration error that fails the send: it never falls back to another
	// account, because delivering from an unintended identity is worse than not
	// delivering. An identifier, never a secret.
	GmailConnectionID string `json:"gmail_connection_id,omitempty"`

	// Legacy per-summary sender overrides. New Notify settings save one
	// GmailConnectionID and clear these fields. A legacy list with multiple
	// senders is refused at delivery rather than sending duplicate messages.
	RunSummaryGmailConnectionIDs   []string `json:"run_summary_gmail_connection_ids,omitempty"`
	PulseSummaryGmailConnectionIDs []string `json:"pulse_summary_gmail_connection_ids,omitempty"`

	// BlockRecipients is a per-workflow email denylist, unioned with the
	// account-wide GmailConfig.BlockedRecipients at send time. It can only block
	// MORE addresses for this workflow, never unblock a globally-blocked one.
	BlockRecipients []string `json:"block_recipients,omitempty"`

	// RunSummaryRecipients and PulseSummaryRecipients say WHERE this workflow's
	// email goes, selected by notify_user's notification_kind — the positive
	// counterpart to BlockRecipients, which only ever says where it must not go.
	// Empty means "inherit the account-level default recipient", so an existing
	// workflow keeps its current behavior. These never widen permission: the
	// account-wide denylist and BlockRecipients are still applied on top and
	// still win, so naming a blocked address here skips the send rather than
	// unblocking it.
	RunSummaryRecipients   []string `json:"run_summary_recipients,omitempty"`
	PulseSummaryRecipients []string `json:"pulse_summary_recipients,omitempty"`
}

const defaultRunSummaryInstructions = "Before external delivery, read prior run summaries with get_notification_history. Always record the current run summary in Activity. Send Slack, Gmail, or WhatsApp only when the current run contains a new materially important outcome, failure, recovery, blocker, decision, or threshold crossing compared with prior summaries. Ignore timestamps, routine counts, and wording-only differences. If nothing important changed, call notify_user with delivery_mode=dashboard_only."

const defaultPulseSummaryInstructions = "Before external delivery, read prior Pulse summaries with get_notification_history and exclude the current pulse_run_id. The current Pulse reviewer results are already recorded in Activity. Send Slack, Gmail, or WhatsApp only for a new materially important finding, fix, recovery, blocker, unsafe backup/publish state, or decision requiring the user. Ignore timestamps and wording-only differences. When external delivery is warranted, call notify_user with notification_kind=pulse_summary and delivery_mode=external_only; otherwise do not send a Pulse notification."

func (c *WorkflowNotificationConfig) EffectiveRunSummaryInstructions() string {
	if c == nil {
		return ""
	}
	if value := strings.TrimSpace(c.RunSummaryInstructions); value != "" {
		return value
	}
	if value := strings.TrimSpace(c.Instructions); value != "" {
		return value
	}
	return ""
}

func (c *WorkflowNotificationConfig) EffectivePulseSummaryInstructions() string {
	if c == nil {
		return ""
	}
	if value := strings.TrimSpace(c.PulseSummaryInstructions); value != "" {
		return value
	}
	if value := strings.TrimSpace(c.Instructions); value != "" {
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

func pulseSummaryInstructionsOrDefault(c *WorkflowNotificationConfig) string {
	if value := c.EffectivePulseSummaryInstructions(); value != "" {
		return value
	}
	return defaultPulseSummaryInstructions
}

// WorkflowExecutionDefaults stores workflow-wide execution settings. Run-folder
// selection is intentionally absent: full runs always use the current slot.
type WorkflowExecutionDefaults struct {
	// Global step overrides (replaces step_override.json)
	DisableLearning              *bool    `json:"disable_learning,omitempty"`
	DisableParallelToolExecution *bool    `json:"disable_parallel_tool_execution,omitempty"`
	EnabledCustomTools           []string `json:"enabled_custom_tools,omitempty"`
	WorkshopMode                 string   `json:"workshop_mode,omitempty"` // Session mode: "workshop" or "run". Every retired name (builder, optimizer, reporting, eval, output, ask, debugger, runner) normalizes to one of those two.
}

// WorkflowSchedule represents a clock or webhook trigger stored in the manifest.
type WorkflowSchedule struct {
	ID             string                 `json:"id"`
	Name           string                 `json:"name"`
	Description    string                 `json:"description,omitempty"`
	ScheduleType   string                 `json:"schedule_type,omitempty"` // "cron" (default), "calendar", or "webhook"
	CronExpression string                 `json:"cron_expression"`
	Timezone       string                 `json:"timezone"`
	Enabled        bool                   `json:"enabled"`
	TriggerPayload json.RawMessage        `json:"trigger_payload,omitempty"`
	CalendarItems  []CalendarScheduleItem `json:"calendar_items,omitempty"`
	GroupNames     []string               `json:"group_names,omitempty"`
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
	DirectMessagesReason string `json:"direct_messages_reason,omitempty"`
	WorkshopMode         string `json:"workshop_mode,omitempty"`   // Writable scheduled work uses Workshop; read-only access is pinned to Run. Legacy values remain parseable.
	Query                string `json:"query,omitempty"`           // Message to execute (multi-agent mode)
	ResumePrevious       *bool  `json:"resume_previous,omitempty"` // Coding-agent CLI only: resume the latest prior thread (same provider) instead of a fresh session each run. nil = default (fresh session); explicit true opts in.
	// PulseReviewOnly is a legacy compatibility field. On read, an enabled
	// legacy entry enables workflow-level pulse.enabled; migration removes the
	// obsolete schedule. The scheduler never registers it as an independent cron.
	PulseReviewOnly bool `json:"pulse_review_only,omitempty"`
	// PulseMode explicitly selects this schedule's post-run stewardship.
	// "off" runs no Pulse actions; "basic" runs only backup, report publish,
	// and the run-summary notification; "full" additionally runs Gate, drift
	// review, review+fix, and Pulse finalization. Empty is legacy-only.
	PulseMode       string `json:"pulse_mode,omitempty"`
	PulseModeReason string `json:"pulse_mode_reason,omitempty"`
	// ExecutionMode is a typed, runtime-enforced operating mode for a scheduled
	// invocation. It is deliberately separate from Messages: safety must not
	// depend on an agent interpreting prose or on the wall clock happening to be
	// inside the expected cron window. Empty means the normal workflow mode.
	ExecutionMode string `json:"execution_mode,omitempty"`
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
		if schedule.ScheduleType == "webhook" || schedule.PulseReviewOnly {
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
	return strings.TrimSpace(schedule.ConcurrencyMode) == "parallel" && schedule.ParallelRiskAcknowledged && schedule.ScheduleType != "webhook" && !schedule.PulseReviewOnly
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
	if m.Kind != "relay" && m.RelayOutputStepID != "" {
		return fmt.Errorf("relay_output_step_id requires kind relay")
	}
	if m.Kind == "relay" && m.Pulse != nil && m.Pulse.Enabled {
		return fmt.Errorf("Relays do not support Pulse")
	}
	if m.Label == "" {
		return fmt.Errorf("label is required")
	}
	if reason := workflowtypes.ValidateIcon(m.Icon); reason != "" {
		return fmt.Errorf("%s", reason)
	}
	if m.RunRetentionCount != nil {
		if *m.RunRetentionCount < 1 || *m.RunRetentionCount > MaxRunRetentionCount {
			return fmt.Errorf("run_retention_count must be between 1 and %d", MaxRunRetentionCount)
		}
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
	if m.Pulse != nil && m.Pulse.AdvisorSpecialization != nil {
		specialization := m.Pulse.AdvisorSpecialization
		if specialization.Version < 1 {
			return fmt.Errorf("pulse.advisor_specialization.version must be >= 1")
		}
		if strings.TrimSpace(specialization.StrategyAuditor) == "" {
			return fmt.Errorf("pulse.advisor_specialization.strategy_auditor is required")
		}
		if strings.TrimSpace(specialization.GoalAdvisor) == "" {
			return fmt.Errorf("pulse.advisor_specialization.goal_advisor is required")
		}
	}
	if m.Pulse != nil {
		if _, err := normalizeDisabledPulseReviewModules(m.Pulse.DisabledReviewModules); err != nil {
			return err
		}
		if err := validateWorkflowPulseSchedule(m.Pulse.Schedule); err != nil {
			return err
		}
		if _, err := resolvePulseAutonomy(m.Pulse.Autonomy); err != nil {
			return err
		}
		if _, err := normalizePulseFocusAreas(m.Pulse.FocusAreas); err != nil {
			return err
		}
	}

	// Validate browser mode if set
	if m.Capabilities.BrowserMode != "" {
		validModes := map[string]bool{
			"none": true, "auto": true, "headless": true, "cdp": true,
		}
		if !validModes[m.Capabilities.BrowserMode] {
			return fmt.Errorf("invalid browser_mode: %s", m.Capabilities.BrowserMode)
		}
	}
	if len(m.Capabilities.CDPPorts) > maxCDPPortsPerRun {
		return fmt.Errorf("capabilities.cdp_ports supports at most %d ports", maxCDPPortsPerRun)
	}
	if len(m.Capabilities.CDPPorts) > 0 && m.Capabilities.BrowserMode != "cdp" && m.Capabilities.BrowserMode != "auto" {
		return fmt.Errorf("capabilities.cdp_ports requires browser_mode %q or %q", "cdp", "auto")
	}
	seenCDPPorts := make(map[int]bool, len(m.Capabilities.CDPPorts))
	for _, port := range m.Capabilities.CDPPorts {
		if port < 1 || port > 65535 {
			return fmt.Errorf("invalid capabilities.cdp_ports entry %d: port must be between 1 and 65535", port)
		}
		if seenCDPPorts[port] {
			return fmt.Errorf("duplicate capabilities.cdp_ports entry: %d", port)
		}
		seenCDPPorts[port] = true
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
		if m.Kind == "relay" && !sched.IsFunctionTrigger() {
			return fmt.Errorf("schedules[%d]: Relays support only function triggers", i)
		}
		if m.Kind == "relay" && len(normalizeScheduleGroupNames(sched.GroupNames)) != 1 {
			return fmt.Errorf("schedules[%d]: a Relay function must select exactly one variable group", i)
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
		// or a PulseReviewOnly schedule (PLAT-115) — the latter never runs the
		// workflow, so it has no group to run.
		if sched.Mode != "multi-agent" && !sched.PulseReviewOnly && len(normalizeScheduleGroupNames(sched.GroupNames)) == 0 {
			return fmt.Errorf("schedules[%d].group_names is required", i)
		}
		if err := validateScheduleRuntimePolicy(sched); err != nil {
			return fmt.Errorf("schedules[%d]: %w", i, err)
		}
		mode := strings.ToLower(strings.TrimSpace(sched.PulseMode))
		if m.Kind == "relay" && mode != "" && mode != schedulePulseModeOff {
			return fmt.Errorf("schedules[%d].pulse_mode must be off for a Relay", i)
		}
		if mode != "" && mode != schedulePulseModeOff && mode != schedulePulseModeBasic && mode != schedulePulseModeFull {
			return fmt.Errorf("schedules[%d].pulse_mode must be off, basic, or full", i)
		}
		if m.Kind != "relay" && schedulepolicy.RequiresExplicitPulse(m.Version) {
			// A persisted legacy "full" is tolerated (it runs as basic); only
			// authoring paths reject newly setting it.
			if err := schedulepolicy.ValidatePulse(schedulepolicy.NormalizePulse(sched.PulseMode), sched.PulseModeReason); err != nil {
				return fmt.Errorf("schedules[%d]: %w", i, err)
			}
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

	// Validate auto-improvement framework enum fields if set.
	if m.OversightMode != "" {
		switch m.OversightMode {
		case OversightManual, OversightSupervised, OversightAutonomous:
		default:
			return fmt.Errorf("invalid oversight_mode: %s", m.OversightMode)
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
			BrowserMode:               "auto",
		},
		ExecutionDefs: WorkflowExecutionDefaults{},
		Schedules:     []WorkflowSchedule{},
		CreatedAt:     now,
		UpdatedAt:     now,
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
	content, exists, err := readFileFromWorkspace(ctx, manifestPath(workspacePath))
	if err != nil {
		return nil, false, fmt.Errorf("failed to read workflow.json: %w", err)
	}
	if !exists {
		return nil, false, nil
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
	llmConfigMigrated := workflowtypes.NormalizePresetLLMConfig(m.Capabilities.LLMConfig)

	// A field retired from the Go schema (e.g. a past execution_defaults knob
	// that no longer does anything) otherwise lingers in workflow.json forever:
	// json.Unmarshal above already silently dropped it from m, but nothing ever
	// rewrites the file to match. Detect that drift on every open/run so stale
	// prose can't sit there implying behavior it no longer has.
	staleTopLevel, staleExecutionDefaults, staleCapabilities := staleManifestFields([]byte(content))
	hasStaleFields := len(staleTopLevel) > 0 || len(staleExecutionDefaults) > 0 || len(staleCapabilities) > 0

	// Persist auto-assigned schedule IDs (and any stale-field prune) so
	// subsequent lookups see the cleaned-up manifest. Skip the write-back when
	// we had to drop a malformed config block on read — rewriting now would
	// silently erase the user's backup/publish config from disk.
	// Crew deliberately shares the AgentWorks runtime reader for capabilities,
	// but its workflow.json is owned by the Work product schema. Rewriting that
	// file through WorkflowManifest drops Crew-only fields such as identity and
	// authenticated triggers (and can narrow its schedule records). Keep this
	// path read-only; Crew's product services own all of its migrations/writes.
	mayPersistManifestMigrations := !isCrewRuntimeManifestWorkspace(workspacePath)
	if mayPersistManifestMigrations && (hadMissingLabel || hadEmptyScheduleID || llmConfigMigrated || hasStaleFields) && len(m.MalformedConfig) == 0 {
		if hasStaleFields {
			log.Printf("[MANIFEST] %s: pruning retired field(s) no longer in schema — top-level=%v execution_defaults=%v capabilities=%v",
				workspacePath, staleTopLevel, staleExecutionDefaults, staleCapabilities)
		}
		if err := WriteWorkflowManifest(ctx, workspacePath, &m); err != nil {
			log.Printf("[WARN] ReadWorkflowManifest: failed to persist manifest migrations for %s: %v", workspacePath, err)
		}
	}
	return &m, true, nil
}

func isCrewRuntimeManifestWorkspace(workspacePath string) bool {
	normalized := "/" + strings.ToLower(strings.Trim(strings.ReplaceAll(strings.TrimSpace(workspacePath), "\\", "/"), "/")) + "/"
	return strings.Contains(normalized, "/chats/work/projects/")
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
// manifest plus its two plain nested config objects, execution_defaults and
// capabilities — the two places retired per-field knobs have historically
// accumulated. backup, publish, pulse, and schedules are deliberately NOT
// inspected here: those are agent-authored blocks that intentionally allow
// shapes the Go structs don't fully model (see stripOptionalConfigBlocks),
// so treating their contents as "stale" would risk deleting live data.
func staleManifestFields(content []byte) (topLevel, executionDefaults, capabilities []string) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(content, &top); err != nil {
		return nil, nil, nil
	}
	topLevel = unknownManifestKeys(top, knownJSONFieldNames(reflect.TypeOf(WorkflowManifest{})))
	if raw, ok := top["execution_defaults"]; ok {
		var obj map[string]json.RawMessage
		if json.Unmarshal(raw, &obj) == nil {
			executionDefaults = unknownManifestKeys(obj, knownJSONFieldNames(reflect.TypeOf(WorkflowExecutionDefaults{})))
		}
	}
	if raw, ok := top["capabilities"]; ok {
		var obj map[string]json.RawMessage
		if json.Unmarshal(raw, &obj) == nil {
			capabilities = unknownManifestKeys(obj, knownJSONFieldNames(reflect.TypeOf(WorkflowCapabilities{})))
		}
	}
	return topLevel, executionDefaults, capabilities
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

// workflowManifestChangelogChanges returns a stable, value-free description of
// a workflow.json mutation. The changelog already carries content hashes as
// the authoritative change boundary; copying values here would make a human
// diff easier at the cost of leaking secrets such as webhook or token values.
// Field paths plus lifecycle markers are sufficient for Artifact Review.
//
// Arrays deliberately appear as one changed field. Their order and contents
// are meaningful to a manifest, but producing index-level entries would be
// noisy and unstable when an item is inserted or reordered.
// withoutPulseSettingsManifestChanges drops changes to Pulse's own settings
// and returns nothing when only those (and updated_at) changed.
func withoutPulseSettingsManifestChanges(changes []step_based_workflow.PlanFieldChange) []step_based_workflow.PlanFieldChange {
	kept := make([]step_based_workflow.PlanFieldChange, 0, len(changes))
	substantive := false
	for _, change := range changes {
		field := strings.TrimSpace(change.Field)
		if field == "workflow.json.pulse" || strings.HasPrefix(field, "workflow.json.pulse.") {
			continue
		}
		if field != "workflow.json.updated_at" {
			substantive = true
		}
		kept = append(kept, change)
	}
	if !substantive {
		return nil
	}
	return kept
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
		m.CodeLayoutVersion = prior.CodeLayoutVersion
	}
	workflowtypes.NormalizePresetLLMConfig(m.Capabilities.LLMConfig)
	// Ensure nil slices become empty arrays in JSON
	ensureManifestSlices(m)

	// Set updated timestamp
	m.UpdatedAt = time.Now().UTC().Format(time.RFC3339)

	if err := ValidateManifest(m); err != nil {
		return fmt.Errorf("manifest validation failed: %w", err)
	}

	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal workflow.json: %w", err)
	}

	if err := writeFileToWorkspace(ctx, manifestPath(workspacePath), string(data)); err != nil {
		return fmt.Errorf("failed to write workflow.json: %w", err)
	}

	// Record the change so artifact drift review can see it. workflow.json has
	// no plan-modification tool, and planning/changelog only ever receives
	// entries from those tools, so until now every edit here was invisible to
	// Artifact Review.
	//
	// Skipped when the bytes are unchanged: WriteWorkflowManifest is called on
	// paths that rewrite the manifest without altering it, and a changelog full
	// of no-op entries would bury the real ones.
	//
	// Pulse's own settings (pulse.*: on/off, schedule, permissions, focus areas,
	// reviewer toggles) are not part of the plan Plan Drift protects, so a save
	// that changes only them is not recorded. Otherwise changing a Pulse
	// setting made the next Pulse spend its pass on a drift review.
	if !previousExists || previous != string(data) {
		if changes := withoutPulseSettingsManifestChanges(workflowManifestChangelogChanges(previous, string(data))); len(changes) > 0 {
			step_based_workflow.LogCanonicalArtifactChange(
				ctx, workspacePath, "write_workflow_manifest",
				"Recorded workflow.json field changes for artifact drift review.",
				changes, workflowManifestChangelogReader, writeFileToWorkspace, createServerLogger(),
				"workflow.json", previous, string(data),
			)
		}
	}
	return nil
}

// SetWorkflowCodeLayoutVersion is the one supported way to change an existing
// workflow's code_layout_version. WriteWorkflowManifest's ordinary update path
// deliberately preserves this field (see the comment above it: "source layout
// is a creation-time contract, not an editable capability") -- this is the
// separate, explicit override for an authorized migration, per
// code-authoring.md's "Deliberate migration to code/" guidance (PLAT-298).
//
// This does not touch, move, or verify any step source itself. The caller (an
// authoring/repair agent following that skill) is responsible for staging a
// complete code/<step-id>/main.py (and script_metadata.json) for every
// regular-type step BEFORE switching to 1 -- the runtime resolves canonical
// source from this field immediately on the next execution, with no
// execution-copy fallback for anything left in learnings/. Nothing under
// learnings/ is ever deleted by this call, so switching back to 0 is an
// instant, safe rollback if something is wrong after a switch to 1.
func SetWorkflowCodeLayoutVersion(ctx context.Context, workspacePath string, version int) error {
	if version != 0 && version != 1 {
		return fmt.Errorf("unsupported code_layout_version %d", version)
	}
	previous, previousExists, readErr := readFileFromWorkspace(ctx, manifestPath(workspacePath))
	if readErr != nil {
		return fmt.Errorf("read existing workflow.json: %w", readErr)
	}
	if !previousExists {
		return fmt.Errorf("workflow.json not found under %s", workspacePath)
	}
	var m WorkflowManifest
	if err := json.Unmarshal([]byte(previous), &m); err != nil {
		return fmt.Errorf("parse existing workflow.json: %w", err)
	}
	if m.CodeLayoutVersion == version {
		return fmt.Errorf("code_layout_version is already %d", version)
	}
	m.CodeLayoutVersion = version
	m.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := ValidateManifest(&m); err != nil {
		return fmt.Errorf("manifest validation failed: %w", err)
	}
	data, err := json.MarshalIndent(&m, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal workflow.json: %w", err)
	}
	if err := writeFileToWorkspace(ctx, manifestPath(workspacePath), string(data)); err != nil {
		return fmt.Errorf("failed to write workflow.json: %w", err)
	}
	step_based_workflow.LogCanonicalArtifactChange(
		ctx, workspacePath, "set_code_layout_version",
		fmt.Sprintf("Switched code_layout_version to %d.", version),
		workflowManifestChangelogChanges(previous, string(data)), workflowManifestChangelogReader, writeFileToWorkspace, createServerLogger(),
		"workflow.json", previous, string(data),
	)
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
		m.Capabilities.BrowserMode = "none"
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

	// Auto-improvement framework defaults. oversight_mode is the one hard-gate
	// field that default-fills — typology, plan-stability, and decision-log
	// handling stay in semantic artifacts and typed Pulse records.
	if m.OversightMode == "" {
		m.OversightMode = OversightSupervised
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
	// List all workspace folders via the workspace API
	folders, err := listWorkspaceFolders(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list workspace folders: %w", err)
	}

	var results []DiscoveredWorkflow
	for _, folder := range folders {
		manifest, exists, err := ReadWorkflowManifest(ctx, folder)
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
