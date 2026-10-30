package server

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/conditiontrigger"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productschedule"
)

// The associated product supplies an entry point, not a detector workspace or
// its credentials. These definitions extend the existing per-person scheduler
// state; targets never acquire caller bindings in their manifests.
type conditionSource struct {
	ProfileID string `json:"profile_id"`
	ProjectID string `json:"project_id"`
	Path      string `json:"path"`
	Label     string `json:"label"`
}

type conditionCallerDefinition struct {
	Source   conditionSource          `json:"source"`
	Schedule productschedule.Schedule `json:"schedule"`
}

func conditionCallerWorkspace(owner, profile, project string) string {
	return path.Join("_users", sanitizeUserIDForPath(owner), "watchers", profile, conditiontrigger.Hash(project))
}

func conditionCallerJob(owner string, def conditionCallerDefinition, state productScheduleUserState) productScheduleJob {
	product := def.Source.ProfileID
	return productScheduleJob{
		UserID: owner, Profile: agentprofiles.Profile{ID: product, Product: product, Name: def.Source.Label},
		ProjectID: def.Source.ProjectID, ProjectTitle: def.Source.Label,
		WorkspacePath: conditionCallerWorkspace(owner, product, def.Source.ProjectID),
		ManifestPath:  productScheduleStatePath(owner), Schedule: def.Schedule, State: state,
		WatcherSource: &def.Source,
	}
}

func (c *conditionService) productSource(ctx context.Context, owner, profile, project string) (productScheduleJob, error) {
	if profile == "workflow" {
		profile = "agentworks"
	}
	if profile == "relay" {
		profile = "relays"
	}
	var source conditionSource
	switch profile {
	case "work":
		if c.s.registry == nil {
			return productScheduleJob{}, errors.New("Crew runtime unavailable")
		}
		p, err := c.s.registry.Resolve("work", 0, owner)
		if err != nil {
			return productScheduleJob{}, err
		}
		binding, err := resolveCrewProjectBinding(ctx, owner, p, project, "")
		if err != nil {
			return productScheduleJob{}, err
		}
		manifest, err := readCrewProjectManifests(ctx, "work", binding.Binding.WorkspacePath)
		if err != nil {
			return productScheduleJob{}, err
		}
		source = conditionSource{ProfileID: "work", ProjectID: manifest.ID, Path: binding.Binding.WorkspacePath, Label: manifest.displayTitle()}
	case "agentworks", "relays":
		workflows, err := DiscoverWorkflowManifests(ctx)
		if err != nil {
			return productScheduleJob{}, err
		}
		for _, w := range filterWorkflowManifestsForUser(&UserClaims{UserID: owner}, workflows) {
			if w.Manifest == nil || (profile == "relays") != (w.Manifest.Kind == "relay") {
				continue
			}
			if w.Manifest.ID != project && w.WorkspacePath != project {
				continue
			}
			if source.ProjectID != "" {
				return productScheduleJob{}, errors.New("watcher source is ambiguous")
			}
			source = conditionSource{ProfileID: profile, ProjectID: w.Manifest.ID, Path: w.WorkspacePath, Label: w.Manifest.Label}
		}
		if source.ProjectID == "" {
			return productScheduleJob{}, errors.New("watcher source is unavailable")
		}
	default:
		return productScheduleJob{}, errors.New("watchers are available in Code, Crew, workflows and Flows")
	}
	return conditionCallerJob(owner, conditionCallerDefinition{Source: source}, productScheduleUserState{}), nil
}

func (c *conditionService) authorizeProductSource(ctx context.Context, job productScheduleJob, cfg conditiontrigger.Config) error {
	if userAccessForClaims(&UserClaims{UserID: job.UserID}).Disabled {
		return conditiontrigger.PermanentError{Err: errors.New("watcher owner's account is disabled")}
	}
	fresh, err := c.productSource(ctx, job.UserID, job.WatcherSource.ProfileID, job.WatcherSource.ProjectID)
	if err != nil {
		return conditiontrigger.PermanentError{Err: err}
	}
	if !workspacePathsMatchForUser(job.UserID, fresh.WatcherSource.Path, job.WatcherSource.Path) {
		return conditiontrigger.PermanentError{Err: errors.New("watcher source changed")}
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	if cfg.GitHubPR != nil {
		if err := c.authorizeGitHubPR(ctx, job); err != nil {
			return err
		}
		if _, err := githubWatcherToken(job.UserID, cfg.GitHubPR); err != nil {
			return err
		}
	}
	// Resolve only the caller's current Vault selection. Never pass the
	// associated product's workspace to credential resolution.
	names := append([]string{}, cfg.Secrets...)
	if err := validateVaultSecretSelection(ctx, job.UserID, nil, &names); err != nil {
		return conditiontrigger.PermanentError{Err: err}
	}
	return nil
}

func (c *conditionService) saveCaller(ctx context.Context, job productScheduleJob, schedule productschedule.Schedule) (productScheduleJob, error) {
	if schedule.CreatedAt == "" {
		schedule.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	def := conditionCallerDefinition{Source: *job.WatcherSource, Schedule: schedule}
	key := projectScheduleStateKey(def.Source.ProfileID, def.Source.ProjectID, schedule.ID)
	err := c.s.updateStateByKey(ctx, job.UserID, key, func(st *productScheduleUserState) {
		st.Watcher = &def
		st.Enabled = nil
		st.ConsecutiveFailures = 0
	})
	if err != nil {
		return productScheduleJob{}, err
	}
	return c.s.Job(ctx, job.UserID, projectScheduleJobID(def.Source.ProfileID, def.Source.ProjectID, schedule.ID))
}

func (c *conditionService) deleteCaller(ctx context.Context, job productScheduleJob) error {
	return c.s.updateStateByKey(ctx, job.UserID, scheduleStateKey(job), func(st *productScheduleUserState) {
		st.Watcher = nil
		paused := false
		st.Enabled = &paused
	})
}

func conditionCallerJobs(owner string, states map[string]productScheduleUserState) []productScheduleJob {
	var jobs []productScheduleJob
	for key, state := range states {
		if state.Watcher == nil {
			continue
		}
		def := *state.Watcher
		if key != projectScheduleStateKey(def.Source.ProfileID, def.Source.ProjectID, def.Schedule.ID) || def.Schedule.Condition == nil {
			continue
		}
		jobs = append(jobs, conditionCallerJob(owner, def, state))
	}
	return jobs
}

// Everyone with product access can see caller attribution. Detector source,
// private cursor and credentials remain with the person who owns the caller.
func (c *conditionService) callersForProduct(ctx context.Context, source productScheduleJob) ([]map[string]interface{}, error) {
	var out []map[string]interface{}
	for _, owner := range conditionCallerOwners() {
		jobs, err := c.s.JobsForUser(internalBotRequestContext(ctx, owner), owner)
		if err != nil {
			return nil, err
		}
		for _, job := range jobs {
			cfg := job.Schedule.Condition
			if cfg == nil || cfg.Target.Kind == "agent" || !workspacePathsMatchForUser(job.UserID, cfg.Target.Name, source.WatcherSource.Path) {
				continue
			}
			entry := map[string]interface{}{"kind": cfg.Target.Kind, "name": cfg.Target.Name, "function": cfg.Target.Function, "trigger_id": cfg.Target.TriggerID, "route_selections": cfg.Target.Routes}
			item := map[string]interface{}{"owner_id": owner, "id": job.Schedule.ID, "name": job.Schedule.Name, "target": entry, "enabled": job.Effective().Enabled, "last_status": job.State.LastStatus, "last_run_at": job.State.LastRunAt}
			ledger, err := c.engine.Read(ctx, conditionKey(job))
			if err != nil {
				return nil, err
			}
			var last time.Time
			for _, row := range ledger.Rows {
				if row.Status == "dispatched" && row.DispatchedAt.After(last) {
					last = row.DispatchedAt
				}
			}
			if !last.IsZero() {
				item["last_dispatch_at"] = last.Format(time.RFC3339)
			}
			if (cfg.Once && !ledger.OnceCompletedAt.IsZero()) || !ledger.CompletedAt.IsZero() {
				item["enabled"] = false
				completed := ledger.OnceCompletedAt
				if completed.IsZero() {
					completed = ledger.CompletedAt
				}
				item["completed_at"] = completed.Format(time.RFC3339)
			}
			out = append(out, item)
			if len(out) >= 200 {
				return out, nil
			}
		}
	}
	if out == nil {
		out = []map[string]interface{}{}
	}
	return out, nil
}

func conditionCallerOwners() []string {
	if !IsMultiUserMode() {
		return []string{GetDefaultUserID()}
	}
	directory, err := loadUserDirectory()
	if err != nil || directory == nil {
		return nil
	}
	var owners []string
	for _, user := range directory.Users {
		if !user.Disabled && user.ID != "" {
			owners = append(owners, user.ID)
		}
	}
	return owners
}

// conditionScriptRoot is the folder a watcher's saved script lives in: the Crew's canonical root, the one a scheduled run reads it
// from. Saving or reading the script through the raw job path found nothing once Crews moved to their canonical root, so updating
// a working watcher was refused as "save the script first".
func conditionScriptRoot(job productScheduleJob) string {
	return canonicalCrewWorkspaceRoot(agentProfileRuntimeWorkspace(job.UserID, job.WorkspacePath))
}

func (c *conditionService) callerScript(ctx context.Context, job productScheduleJob, cfg *conditiontrigger.Config, trigger map[string]interface{}) (string, error) {
	if job.WatcherSource != nil {
		// The server chooses the path: a script cannot replace another
		// watcher's source or read/write the associated product's files.
		cfg.Script = path.Join("code/triggers", job.Schedule.ID+".py")
		if content, ok := trigger["script_content"]; ok {
			text, ok := content.(string)
			if !ok || strings.TrimSpace(text) == "" || len(text) > conditiontrigger.MaxOutput {
				return "", errors.New("script_content must be Python text of at most 64 KiB")
			}
			return text, nil
		}
	}
	if job.WatcherSource == nil {
		if _, ok := trigger["script_content"]; ok {
			return "", errors.New("Code watchers use a saved Python file under code/triggers")
		}
	}
	raw, found, err := c.s.readFile(ctx, path.Join(conditionScriptRoot(job), cfg.Script))
	if err != nil {
		return "", err
	}
	if !found {
		return "", fmt.Errorf("the watcher script %s was not found in this project; save the Python file there first (script_content is only for mail watchers)", cfg.Script)
	}
	return raw, nil
}

func (c *conditionService) readCallerScript(ctx context.Context, job productScheduleJob) (interface{}, error) {
	if job.Schedule.Condition.GitHubPR != nil {
		return map[string]interface{}{"github_pr": job.Schedule.Condition.GitHubPR}, nil
	}
	raw, found, err := c.s.readFile(ctx, path.Join(conditionScriptRoot(job), job.Schedule.Condition.Script))
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, errors.New("watcher script is unavailable")
	}
	return map[string]interface{}{"script": job.Schedule.Condition.Script, "content": raw, "sha256": conditiontrigger.Hash(raw)}, nil
}
