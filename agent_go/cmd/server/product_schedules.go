package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/codeproduct"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productschedule"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
)

// Product schedules are the recurring jobs a product declares in its
// product.yaml (profile.schedules). For every user who has the product, the
// platform runs each due schedule by sending its messages one at a time into
// the user's normal product conversation, unless the saved schedule selects
// its own persistent isolated conversation.
// Each user keeps their own enable/disable override and run bookkeeping in
// _users/<id>/chat_history/product-schedules.json; run history goes to
// schedule-runs.json next to the conversation, the same file workflow
// schedules use.
//
// This deliberately stays outside SchedulerService: that service is built
// around workflow manifests (leases, dependency queues, capacity waits, Pulse
// reviews). A product schedule needs none of that; it needs the timing rule
// and the one-message-at-a-time send, which productschedule and
// startSessionInternal already provide.

const productScheduleJobPrefix = "product:"
const projectScheduleJobPrefix = "product-project:"
const productScheduleStateFile = "product-schedules.json"

const (
	runDestinationCrewChat = "crew_chat"
	runDestinationIsolated = "isolated"
)

func runDestination(isolated bool) string {
	if isolated {
		return runDestinationIsolated
	}
	return runDestinationCrewChat
}

func isolatedForRunDestination(value string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", runDestinationCrewChat:
		return false, nil
	case runDestinationIsolated:
		return true, nil
	default:
		return false, fmt.Errorf("run_destination must be %q or %q", runDestinationCrewChat, runDestinationIsolated)
	}
}

// productScheduleUserState is one user's bookkeeping for one schedule.
type productScheduleUserState struct {
	// Enabled overrides the product's default when set.
	Enabled *bool `json:"enabled,omitempty"`
	// CadenceHours overrides a cadence schedule's interval for this person (for example Organize Brain every 72h
	// instead of weekly, PLAT-618). Ignored for cron and one-time schedules.
	CadenceHours *int `json:"cadence_hours,omitempty"`
	// Messages replaces the schedule's messages for this person (the Brain chat's brain_schedule set_message).
	Messages []string `json:"messages,omitempty"`
	// ActivatedAt is the persisted first-seen baseline for legacy/built-in
	// cron definitions that predate Schedule.CreatedAt.
	ActivatedAt string `json:"activated_at,omitempty"`
	LastRunAt   string `json:"last_run_at,omitempty"`
	// LastAttemptAt is when a run last started, successful or not; with
	// ConsecutiveFailures it feeds the retry backoff (a failed check-in must
	// not re-fire on every tick because only success moves LastRunAt).
	LastAttemptAt       string `json:"last_attempt_at,omitempty"`
	LastStatus          string `json:"last_status,omitempty"`
	LastError           string `json:"last_error,omitempty"`
	LastSessionID       string `json:"last_session_id,omitempty"`
	LastDurationMs      *int64 `json:"last_duration_ms,omitempty"`
	RunCount            int    `json:"run_count,omitempty"`
	ConsecutiveFailures int    `json:"consecutive_failures,omitempty"`
}

// productScheduleJob is one (profile, schedule, user) triple.
type productScheduleJob struct {
	UserID            string
	GuestCallerID     string // a non-owner's call: the turn runs as their guest
	Profile           agentprofiles.Profile
	Schedule          productschedule.Schedule
	State             productScheduleUserState
	ProjectID         string
	ProjectTitle      string
	CodeCaller        *triggerCaller // private Code function source, rechecked before a queued turn
	CodeCallerPath    string
	ProjectCaller     *triggerCaller
	ProjectCallerPath string
	// ConversationKey narrows an isolated run's conversation to one calling
	// person (a cross-owner Crew call); empty keeps one per schedule/trigger.
	ConversationKey string
	WorkspacePath   string
	ManifestPath    string
	AutomationKind  string
	// ManifestActivatedAt migrates legacy project schedules that have no
	// per-schedule CreatedAt without moving their first occurrence to each
	// scheduler tick.
	ManifestActivatedAt time.Time
}

// ID is the job id exposed through /api/scheduler/jobs.
func (j productScheduleJob) ID() string {
	if j.ProjectID != "" {
		return projectScheduleJobID(j.Profile.ID, j.ProjectID, j.Schedule.ID)
	}
	return productScheduleJobID(j.Profile.ID, j.Schedule.ID)
}

// Effective returns the schedule with the user's enable override applied.
func (j productScheduleJob) Effective() productschedule.Schedule {
	s := j.Schedule
	if j.State.Enabled != nil {
		s.Enabled = *j.State.Enabled
	}
	if j.State.CadenceHours != nil && *j.State.CadenceHours > 0 && s.CadenceHours > 0 {
		s.CadenceHours = *j.State.CadenceHours
	}
	if len(j.State.Messages) > 0 {
		s.Messages = append([]string(nil), j.State.Messages...)
	}
	return s
}

func (j productScheduleJob) lastRun() time.Time     { return parseRFC3339OrZero(j.State.LastRunAt) }
func (j productScheduleJob) lastAttempt() time.Time { return parseRFC3339OrZero(j.State.LastAttemptAt) }

func (j productScheduleJob) activatedAt() time.Time {
	if activated := parseRFC3339OrZero(j.State.ActivatedAt); !activated.IsZero() {
		return activated
	}
	if created := parseRFC3339OrZero(j.Schedule.CreatedAt); !created.IsZero() {
		return created
	}
	return j.ManifestActivatedAt
}

func parseRFC3339OrZero(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

func productScheduleJobID(profileID, scheduleID string) string {
	return productScheduleJobPrefix + strings.TrimSpace(profileID) + ":" + strings.TrimSpace(scheduleID)
}

func projectScheduleJobID(profileID, projectID, scheduleID string) string {
	return projectScheduleJobPrefix + strings.TrimSpace(profileID) + ":" + strings.TrimSpace(projectID) + ":" + strings.TrimSpace(scheduleID)
}

func parseProjectScheduleJobID(id string) (profileID, projectID, scheduleID string, ok bool) {
	if !strings.HasPrefix(id, projectScheduleJobPrefix) {
		return "", "", "", false
	}
	parts := strings.Split(strings.TrimPrefix(id, projectScheduleJobPrefix), ":")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return "", "", "", false
	}
	return parts[0], parts[1], parts[2], true
}

// parseProductScheduleJobID splits "product:<profile>:<schedule>".
func parseProductScheduleJobID(id string) (profileID, scheduleID string, ok bool) {
	if !strings.HasPrefix(id, productScheduleJobPrefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(id, productScheduleJobPrefix)
	i := strings.LastIndex(rest, ":")
	if i <= 0 || i == len(rest)-1 {
		return "", "", false
	}
	return rest[:i], rest[i+1:], true
}

func productScheduleStatePath(userID string) string {
	return filepath.ToSlash(filepath.Join(chatHistoryRoot(userID), productScheduleStateFile))
}

func productScheduleStateKey(profileID, scheduleID string) string {
	return strings.TrimSpace(profileID) + "/" + strings.TrimSpace(scheduleID)
}

func projectScheduleStateKey(profileID, projectID, scheduleID string) string {
	return strings.TrimSpace(profileID) + "/" + strings.TrimSpace(projectID) + "/" + strings.TrimSpace(scheduleID)
}

func scheduleStateKey(job productScheduleJob) string {
	if job.ProjectID != "" {
		return projectScheduleStateKey(job.Profile.ID, job.ProjectID, job.Schedule.ID)
	}
	return productScheduleStateKey(job.Profile.ID, job.Schedule.ID)
}

// ProductScheduleService runs product schedules for every user.
type ProductScheduleService struct {
	api      *StreamingAPI
	registry *agentprofiles.Registry

	// users lists the user ids that may have product schedules; swapped in tests.
	users func(product string) []string
	// sinceInteractive is how long ago a user last used the product; nil
	// means the quiet rule never holds a run back on the platform.
	sinceInteractive func(userID string, profile agentprofiles.Profile) time.Duration
	// readFile / writeFile back the per-user state file; swapped in tests.
	readFile  func(context.Context, string) (string, bool, error)
	writeFile func(context.Context, string, string) error
	// crewAvailability backs the CreateCrewProject availability checks; nil
	// selects the live MCP/secret lookups, and tests swap individual probes.
	crewAvailability *crewCreationAvailability

	mu      sync.Mutex
	running map[string]*productScheduleRun // key: userID + "\x1f" + job id
	// conversations holds one claim per live automation conversation, so two
	// turns never interleave in the same conversation. Key: userID + "\x1f" +
	// conversation key. s.running stays per job for Running/Stop lookups.
	conversations map[string]bool
	// heldConversation reports a conversation as busy without a claim, so
	// deliveries queue instead of starting a live agent turn; tests only.
	heldConversation func(convKey string) bool
	// queued holds accepted deliveries waiting their turn, FIFO per conversation key.
	queued map[string][]productScheduleQueuedRun
	// deferred holds the quiet-rule reason for jobs currently held back, so
	// the UI can say "waiting for a quiet moment" instead of showing nothing.
	deferred map[string]string
	stateMu  sync.Mutex
}

// productInteractions is the shared record of who last used which product;
// the product chat handlers stamp it and the quiet rule reads it.
var productInteractions = newProductInteractionTracker()

type productScheduleRun struct {
	SessionID string
	StartedAt time.Time
	cancel    context.CancelFunc
}

// NewProductScheduleService wires the service to the live profile registry.
func NewProductScheduleService(api *StreamingAPI, registry *agentprofiles.Registry) *ProductScheduleService {
	svc := &ProductScheduleService{api: api, registry: registry, running: map[string]*productScheduleRun{}, deferred: map[string]string{}, conversations: map[string]bool{}, queued: map[string][]productScheduleQueuedRun{}}
	svc.users = usersWithProduct
	svc.readFile = readFileFromWorkspace
	svc.writeFile = writeFileToWorkspace
	svc.sinceInteractive = func(userID string, profile agentprofiles.Profile) time.Duration {
		return productInteractions.SinceInteractive(context.Background(), userID, profile.Product)
	}
	return svc
}

func (s *ProductScheduleService) setDeferred(userID, jobID, reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.deferred == nil {
		s.deferred = map[string]string{}
	}
	key := userID + "\x1f" + jobID
	if reason == "" {
		delete(s.deferred, key)
		return
	}
	s.deferred[key] = reason
}

func (s *ProductScheduleService) deferredReason(userID, jobID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deferred[userID+"\x1f"+jobID]
}

// usersWithProduct lists the users a product's schedules run for: every
// enabled directory user whose product access includes it, or the single
// local user when the server is not multi-user.
func usersWithProduct(product string) []string {
	if !IsMultiUserMode() {
		return []string{GetDefaultUserID()}
	}
	dir, err := loadUserDirectory()
	if err != nil || dir == nil {
		return nil
	}
	var out []string
	for i := range dir.Users {
		rec := &dir.Users[i]
		if rec.Disabled || strings.TrimSpace(rec.ID) == "" {
			continue
		}
		if userAccessAllowsProduct(accessForRecord(rec), product) {
			out = append(out, rec.ID)
		}
	}
	sort.Strings(out)
	return out
}

func userAccessAllowsProduct(acc UserAccess, product string) bool {
	if adminOnlyProduct(product) && !acc.Admin {
		return false
	}
	if !acc.ProductsRestricted {
		return true
	}
	for _, p := range acc.Products {
		if strings.EqualFold(strings.TrimSpace(p), strings.TrimSpace(product)) {
			return true
		}
	}
	return false
}

func productAccessName(profile agentprofiles.Profile) string {
	if name := strings.TrimSpace(profile.Product); name != "" {
		return name
	}
	return strings.TrimSpace(profile.ID)
}

// profilesWithSchedules returns the built-in profiles that declare schedules.
func (s *ProductScheduleService) profilesWithSchedules() []agentprofiles.Profile {
	if s.registry == nil {
		return nil
	}
	var out []agentprofiles.Profile
	for _, p := range s.registry.List("") {
		if p.BuiltIn && len(p.Schedules) > 0 {
			out = append(out, p)
		}
	}
	return out
}

func (s *ProductScheduleService) profilesWithProjectSchedules() []agentprofiles.Profile {
	if s.registry == nil {
		return nil
	}
	var out []agentprofiles.Profile
	for _, p := range s.registry.List("") {
		if p.BuiltIn && p.UIPanels.Schedules && strings.EqualFold(strings.TrimSpace(p.Runtime.Conversation.Mode), agentprofiles.ConversationModeKeyed) {
			out = append(out, p)
		}
	}
	return out
}

func (s *ProductScheduleService) projectJobsForUser(ctx context.Context, userID string, profile agentprofiles.Profile, states map[string]productScheduleUserState) ([]productScheduleJob, error) {
	// The user's own projects root and, for a Crew, the Crew/ shared root (PLAT-442 step 4): a migrated Crew's
	// schedules must keep firing for its owner.
	store := defaultProductProjectStore()
	paths, exists, err := listProjectManifestPaths(ctx, store, userID, profile)
	if err != nil || !exists {
		return nil, err
	}
	var jobs []productScheduleJob
	for _, candidate := range paths {
		raw, found, readErr := store.read(ctx, candidate)
		if readErr != nil {
			return nil, readErr
		}
		if !found {
			continue
		}
		var manifest productProjectManifest
		if err := json.Unmarshal([]byte(raw), &manifest); err != nil || manifest.Product != profile.ID || strings.TrimSpace(manifest.ID) == "" {
			continue
		}
		manifestActivatedAt := parseRFC3339OrZero(manifest.UpdatedAt)
		if manifestActivatedAt.IsZero() {
			manifestActivatedAt = parseRFC3339OrZero(manifest.CreatedAt)
		}
		runtimePath := candidate
		if isProjectProfileID(profile.ID) {
			runtimePath = projectRuntimeManifestPath(profile.ID, filepath.ToSlash(filepath.Dir(candidate)))
			if runtimeRaw, runtimeFound, runtimeErr := s.readFile(ctx, runtimePath); runtimeErr != nil {
				return nil, runtimeErr
			} else if runtimeFound {
				var runtimeManifest productProjectManifest
				if err := json.Unmarshal([]byte(runtimeRaw), &runtimeManifest); err != nil {
					return nil, fmt.Errorf("project %s runtime manifest: %w", manifest.ID, err)
				}
				manifest.Schedules = runtimeManifest.Schedules
				manifest.Triggers = runtimeManifest.Triggers
				manifest.SharedKnowledgebase = runtimeManifest.SharedKnowledgebase
				manifest.KnowledgebaseMode = runtimeManifest.KnowledgebaseMode
				manifest.BrainAccess = runtimeManifest.BrainAccess
				manifest.KnowledgebaseMigration = runtimeManifest.KnowledgebaseMigration
				manifest.KnowledgebaseContractHistory = runtimeManifest.KnowledgebaseContractHistory
				manifest.Capabilities = runtimeManifest.Capabilities
				if runtimeUpdatedAt := parseRFC3339OrZero(runtimeManifest.UpdatedAt); !runtimeUpdatedAt.IsZero() {
					manifestActivatedAt = runtimeUpdatedAt
				} else if runtimeCreatedAt := parseRFC3339OrZero(runtimeManifest.CreatedAt); !runtimeCreatedAt.IsZero() {
					manifestActivatedAt = runtimeCreatedAt
				}
			}
		}
		if err := productschedule.ValidateAll(manifest.Schedules); err != nil {
			return nil, fmt.Errorf("project %s schedules: %w", manifest.ID, err)
		}
		for _, sched := range manifest.Schedules {
			job := productScheduleJob{
				UserID: userID, Profile: profile, Schedule: sched,
				ProjectID: manifest.ID, ProjectTitle: manifest.displayTitle(),
				WorkspacePath: filepath.ToSlash(filepath.Dir(candidate)), ManifestPath: runtimePath,
				ManifestActivatedAt: manifestActivatedAt,
			}
			job.State = states[scheduleStateKey(job)]
			jobs = append(jobs, job)
		}
	}
	return jobs, nil
}

func (s *ProductScheduleService) loadState(ctx context.Context, userID string) (map[string]productScheduleUserState, error) {
	content, exists, err := s.readFile(ctx, productScheduleStatePath(userID))
	if err != nil {
		return nil, err
	}
	out := map[string]productScheduleUserState{}
	if !exists || strings.TrimSpace(content) == "" {
		return out, nil
	}
	if err := json.Unmarshal([]byte(content), &out); err != nil {
		return nil, fmt.Errorf("parse %s: %w", productScheduleStateFile, err)
	}
	return out, nil
}

// JobsForUser lists every product schedule visible to one user.
func (s *ProductScheduleService) JobsForUser(ctx context.Context, userID string) ([]productScheduleJob, error) {
	if s == nil || strings.TrimSpace(userID) == "" {
		return nil, nil
	}
	var jobs []productScheduleJob
	var states map[string]productScheduleUserState
	for _, profile := range s.profilesWithSchedules() {
		if !containsUserID(s.users(productAccessName(profile)), userID) {
			continue
		}
		if states == nil {
			var err error
			if states, err = s.loadState(ctx, userID); err != nil {
				return nil, err
			}
		}
		for _, sched := range profile.Schedules {
			jobs = append(jobs, productScheduleJob{
				UserID:   userID,
				Profile:  profile,
				Schedule: sched,
				State:    states[productScheduleStateKey(profile.ID, sched.ID)],
			})
		}
	}
	for _, profile := range s.profilesWithProjectSchedules() {
		if !containsUserID(s.users(productAccessName(profile)), userID) {
			continue
		}
		if states == nil {
			var err error
			if states, err = s.loadState(ctx, userID); err != nil {
				return nil, err
			}
		}
		projectJobs, err := s.projectJobsForUser(ctx, userID, profile, states)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, projectJobs...)
	}
	return jobs, nil
}

func containsUserID(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// Job finds one product schedule for a user by job id.
func (s *ProductScheduleService) Job(ctx context.Context, userID, jobID string) (productScheduleJob, error) {
	profileID, scheduleID, ok := parseProductScheduleJobID(jobID)
	projectID := ""
	if projectProfileID, parsedProjectID, projectScheduleID, projectOK := parseProjectScheduleJobID(jobID); projectOK {
		profileID, projectID, scheduleID, ok = projectProfileID, parsedProjectID, projectScheduleID, true
	}
	if !ok {
		return productScheduleJob{}, fmt.Errorf("not a product schedule id: %q", jobID)
	}
	jobs, err := s.JobsForUser(ctx, userID)
	if err != nil {
		return productScheduleJob{}, err
	}
	for _, j := range jobs {
		if j.Profile.ID == profileID && j.ProjectID == projectID && j.Schedule.ID == scheduleID {
			return j, nil
		}
	}
	return productScheduleJob{}, fmt.Errorf("product schedule %s not found for this user", jobID)
}

// SetEnabled stores a user's enable override.
func (s *ProductScheduleService) SetEnabled(ctx context.Context, userID, jobID string, enabled bool) (productScheduleJob, error) {
	job, err := s.Job(ctx, userID, jobID)
	if err != nil {
		return job, err
	}
	if err := s.updateStateByKey(ctx, userID, scheduleStateKey(job), func(st *productScheduleUserState) {
		st.Enabled = &enabled
	}); err != nil {
		return job, err
	}
	return s.Job(ctx, userID, jobID)
}

// Cadence limits for a person's override: at least every hour, at most every 90 days.
const minScheduleCadenceHours, maxScheduleCadenceHours = 1, 24 * 90

// SetCadence sets one person's interval for a cadence schedule (PLAT-618).
func (s *ProductScheduleService) SetCadence(ctx context.Context, userID, jobID string, hours int) (productScheduleJob, error) {
	job, err := s.Job(ctx, userID, jobID)
	if err != nil {
		return job, err
	}
	if job.Schedule.CadenceHours <= 0 {
		return job, fmt.Errorf("schedule %q does not run on a cadence", jobID)
	}
	if hours < minScheduleCadenceHours || hours > maxScheduleCadenceHours {
		return job, fmt.Errorf("cadence must be between %d and %d hours", minScheduleCadenceHours, maxScheduleCadenceHours)
	}
	if err := s.updateStateByKey(ctx, userID, scheduleStateKey(job), func(st *productScheduleUserState) {
		st.CadenceHours = &hours
	}); err != nil {
		return job, err
	}
	return s.Job(ctx, userID, jobID)
}

// maxScheduleMessageBytes bounds a person's own schedule message.
const maxScheduleMessageBytes = 8000

// SetMessage sets what one person's run of a schedule asks for; empty restores the product's message (PLAT-618).
func (s *ProductScheduleService) SetMessage(ctx context.Context, userID, jobID, message string) (productScheduleJob, error) {
	job, err := s.Job(ctx, userID, jobID)
	if err != nil {
		return job, err
	}
	message = strings.TrimSpace(message)
	if len(message) > maxScheduleMessageBytes {
		return job, fmt.Errorf("the schedule message is limited to %d bytes", maxScheduleMessageBytes)
	}
	if err := s.updateStateByKey(ctx, userID, scheduleStateKey(job), func(st *productScheduleUserState) {
		st.Messages = nil
		if message != "" {
			st.Messages = []string{message}
		}
	}); err != nil {
		return job, err
	}
	return s.Job(ctx, userID, jobID)
}

// projectManifestAnyOwner resolves a crew project under the caller first
// (with the usual legacy migration), then under whichever other owner
// holds it. The fallback is read-only: a reader's workflow run must never
// rewrite the owner's manifests, so legacy conversion is skipped there.
// A project ID present under two owners fails closed as ambiguous.
func (s *ProductScheduleService) projectManifestAnyOwner(ctx context.Context, userID, profileID, projectID string) (agentprofiles.Profile, productConversationBinding, productProjectManifest, string, error) {
	failed := func(err error) (agentprofiles.Profile, productConversationBinding, productProjectManifest, string, error) {
		return agentprofiles.Profile{}, productConversationBinding{}, productProjectManifest{}, "", err
	}
	profile, binding, manifest, err := s.projectManifest(ctx, userID, profileID, projectID)
	if err == nil {
		return profile, binding, manifest, sanitizeUserIDForPath(userID), nil
	}
	firstErr := err
	if s.registry == nil {
		return failed(firstErr)
	}
	profile, resolveErr := s.registry.Resolve(profileID, 0, userID)
	if resolveErr != nil {
		return failed(firstErr)
	}
	if !profile.UIPanels.Schedules || !strings.EqualFold(strings.TrimSpace(profile.Runtime.Conversation.Mode), agentprofiles.ConversationModeKeyed) {
		return failed(firstErr)
	}
	want := strings.TrimSpace(projectID)
	var matchOwner string
	var matchBinding productConversationBinding
	var matchManifest productProjectManifest
	matched := false
	for _, owner := range crewProjectOwnerCandidates(userID) {
		binding, bindErr := resolveProductProjectBindingWithStore(ctx, owner, profile, want, defaultProductProjectStore())
		if bindErr != nil {
			continue
		}
		manifest, manifestErr := readCrewProjectManifests(ctx, profile.ID, binding.WorkspacePath)
		if manifestErr != nil || strings.TrimSpace(manifest.ID) != want {
			continue
		}
		ownerID, ok := crewProjectOwnerID(binding.WorkspacePath)
		if !ok {
			continue
		}
		if matched {
			return failed(fmt.Errorf("crew project %q is ambiguous", want))
		}
		matchOwner, matchBinding, matchManifest, matched = ownerID, binding, manifest, true
	}
	if !matched {
		return failed(firstErr)
	}
	return profile, matchBinding, matchManifest, matchOwner, nil
}

func (s *ProductScheduleService) projectManifest(ctx context.Context, userID, profileID, projectID string) (agentprofiles.Profile, productConversationBinding, productProjectManifest, error) {
	if s.registry == nil {
		return agentprofiles.Profile{}, productConversationBinding{}, productProjectManifest{}, fmt.Errorf("product profiles are unavailable")
	}
	profile, err := s.registry.Resolve(profileID, 0, userID)
	if err != nil {
		return agentprofiles.Profile{}, productConversationBinding{}, productProjectManifest{}, err
	}
	if !profile.UIPanels.Schedules || !strings.EqualFold(strings.TrimSpace(profile.Runtime.Conversation.Mode), agentprofiles.ConversationModeKeyed) {
		return agentprofiles.Profile{}, productConversationBinding{}, productProjectManifest{}, fmt.Errorf("profile %q does not support project schedules", profileID)
	}
	binding, err := resolveProductConversationBinding(ctx, userID, profile, projectID)
	if err != nil {
		return agentprofiles.Profile{}, productConversationBinding{}, productProjectManifest{}, err
	}
	metadataRaw, found, err := s.readFile(ctx, binding.ManifestPath)
	if err != nil || !found {
		if err == nil {
			err = fmt.Errorf("project manifest not found")
		}
		return agentprofiles.Profile{}, productConversationBinding{}, productProjectManifest{}, err
	}
	var metadata productProjectManifest
	if err := json.Unmarshal([]byte(metadataRaw), &metadata); err != nil {
		return agentprofiles.Profile{}, productConversationBinding{}, productProjectManifest{}, err
	}
	manifest := metadata
	if isProjectProfileID(profileID) {
		runtimePath := projectRuntimeManifestPath(profileID, binding.WorkspacePath)
		runtimeRaw, runtimeFound, runtimeErr := s.readFile(ctx, runtimePath)
		if runtimeErr != nil {
			return agentprofiles.Profile{}, productConversationBinding{}, productProjectManifest{}, runtimeErr
		}
		if !runtimeFound {
			legacy, conversionErr := workRuntimeManifestFromLegacy(metadataRaw)
			if conversionErr != nil {
				return agentprofiles.Profile{}, productConversationBinding{}, productProjectManifest{}, conversionErr
			}
			encoded, conversionErr := json.MarshalIndent(legacy, "", "  ")
			if conversionErr != nil {
				return agentprofiles.Profile{}, productConversationBinding{}, productProjectManifest{}, conversionErr
			}
			runtimeRaw = string(encoded) + "\n"
			if conversionErr := s.writeFile(ctx, runtimePath, runtimeRaw); conversionErr != nil {
				return agentprofiles.Profile{}, productConversationBinding{}, productProjectManifest{}, conversionErr
			}
			var metadataDocument map[string]interface{}
			if json.Unmarshal([]byte(metadataRaw), &metadataDocument) == nil {
				for _, key := range []string{"capabilities", "schedules", "triggers", "workflow_context_paths"} {
					delete(metadataDocument, key)
				}
				metadataDocument["updated_at"] = time.Now().UTC().Format(time.RFC3339)
				if encodedMetadata, encodeErr := json.MarshalIndent(metadataDocument, "", "  "); encodeErr == nil {
					_ = s.writeFile(ctx, binding.ManifestPath, string(encodedMetadata)+"\n")
				}
			}
		}
		var runtimeManifest productProjectManifest
		if err := json.Unmarshal([]byte(runtimeRaw), &runtimeManifest); err != nil {
			return agentprofiles.Profile{}, productConversationBinding{}, productProjectManifest{}, err
		}
		manifest.Capabilities = runtimeManifest.Capabilities
		manifest.Schedules = runtimeManifest.Schedules
		manifest.Triggers = runtimeManifest.Triggers
		manifest.SharedKnowledgebase = runtimeManifest.SharedKnowledgebase
		manifest.KnowledgebaseMode = runtimeManifest.KnowledgebaseMode
		manifest.BrainAccess = runtimeManifest.BrainAccess
		manifest.KnowledgebaseMigration = runtimeManifest.KnowledgebaseMigration
		manifest.KnowledgebaseContractHistory = runtimeManifest.KnowledgebaseContractHistory
		manifest.WorkflowContextPaths = runtimeManifest.WorkflowContextPaths
		binding.ManifestPath = runtimePath
	}
	return profile, binding, manifest, nil
}

func (s *ProductScheduleService) writeProjectManifest(ctx context.Context, binding productConversationBinding, manifest productProjectManifest) error {
	manifest.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if filepath.Base(filepath.FromSlash(binding.ManifestPath)) == "workflow.json" {
		if strings.TrimSpace(manifest.Label) == "" {
			manifest.Label = manifest.Title
		}
		manifest.Product = ""
		manifest.Title = ""
		manifest.Description = ""
		manifest.SessionID = ""
		manifest.Capabilities.WorkflowContextPaths = nil
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	return s.writeFile(ctx, binding.ManifestPath, string(data)+"\n")
}

func (s *ProductScheduleService) CreateProjectSchedule(ctx context.Context, userID, profileID, projectID string, schedule productschedule.Schedule) (productScheduleJob, error) {
	profile, binding, manifest, err := s.projectManifest(ctx, userID, profileID, projectID)
	if err != nil {
		return productScheduleJob{}, err
	}
	if strings.TrimSpace(schedule.ID) == "" {
		schedule.ID = uuid.NewString()
	}
	if strings.TrimSpace(schedule.CreatedAt) == "" {
		schedule.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	manifest.Schedules = append(manifest.Schedules, schedule)
	if err := productschedule.ValidateAll(manifest.Schedules); err != nil {
		return productScheduleJob{}, err
	}
	if err := s.writeProjectManifest(ctx, binding, manifest); err != nil {
		return productScheduleJob{}, err
	}
	return productScheduleJob{UserID: userID, Profile: profile, Schedule: schedule, ProjectID: manifest.ID, ProjectTitle: manifest.displayTitle(), WorkspacePath: binding.WorkspacePath, ManifestPath: binding.ManifestPath}, nil
}

func (s *ProductScheduleService) UpdateProjectSchedule(ctx context.Context, userID, jobID string, update func(*productschedule.Schedule)) (productScheduleJob, error) {
	job, err := s.Job(ctx, userID, jobID)
	if err != nil || job.ProjectID == "" {
		return job, firstError(err, fmt.Errorf("not a project schedule"))
	}
	_, binding, manifest, err := s.projectManifest(ctx, userID, job.Profile.ID, job.ProjectID)
	if err != nil {
		return job, err
	}
	found := false
	for i := range manifest.Schedules {
		if manifest.Schedules[i].ID == job.Schedule.ID {
			update(&manifest.Schedules[i])
			job.Schedule = manifest.Schedules[i]
			found = true
			break
		}
	}
	if !found {
		return job, fmt.Errorf("schedule not found")
	}
	if err := productschedule.ValidateAll(manifest.Schedules); err != nil {
		return job, err
	}
	if err := s.writeProjectManifest(ctx, binding, manifest); err != nil {
		return job, err
	}
	return s.Job(ctx, userID, jobID)
}

func (s *ProductScheduleService) DeleteProjectSchedule(ctx context.Context, userID, jobID string) error {
	job, err := s.Job(ctx, userID, jobID)
	if err != nil {
		return err
	}
	if job.ProjectID == "" {
		return fmt.Errorf("not a project schedule")
	}
	_, binding, manifest, err := s.projectManifest(ctx, userID, job.Profile.ID, job.ProjectID)
	if err != nil {
		return err
	}
	next := manifest.Schedules[:0]
	for _, schedule := range manifest.Schedules {
		if schedule.ID != job.Schedule.ID {
			next = append(next, schedule)
		}
	}
	if len(next) == len(manifest.Schedules) {
		return fmt.Errorf("schedule not found")
	}
	manifest.Schedules = next
	return s.writeProjectManifest(ctx, binding, manifest)
}

func firstError(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *ProductScheduleService) updateStateByKey(ctx context.Context, userID, key string, fn func(*productScheduleUserState)) error {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	all, err := s.loadState(ctx, userID)
	if err != nil {
		return err
	}
	st := all[key]
	fn(&st)
	all[key] = st
	data, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return err
	}
	return s.writeFile(ctx, productScheduleStatePath(userID), string(data))
}

// Start runs the tick loop until ctx is done.
func (s *ProductScheduleService) Start(ctx context.Context) {
	if len(s.profilesWithSchedules()) == 0 && len(s.profilesWithProjectSchedules()) == 0 {
		scheduleLogf("[PRODUCT-SCHEDULE] No product declares or supports schedules; loop idle")
	}
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			s.tick(ctx, now)
		}
	}
}

func (s *ProductScheduleService) tick(ctx context.Context, now time.Time) {
	users := map[string]struct{}{}
	profiles := append(s.profilesWithSchedules(), s.profilesWithProjectSchedules()...)
	for _, profile := range profiles {
		for _, userID := range s.users(productAccessName(profile)) {
			users[userID] = struct{}{}
		}
	}
	for userID := range users {
		jobs, err := s.JobsForUser(ctx, userID)
		if err != nil {
			scheduleLogf("[PRODUCT-SCHEDULE] %s: cannot discover schedules: %v", userID, err)
			continue
		}
		for _, job := range jobs {
			activatedAt := job.activatedAt()
			if strings.TrimSpace(job.Schedule.CronExpression) != "" && job.lastRun().IsZero() && strings.TrimSpace(job.State.ActivatedAt) == "" {
				// Persist the baseline once. Using this tick's Now without saving it
				// would make a never-run cron schedule chase tomorrow forever.
				if activatedAt.IsZero() {
					activatedAt = now.UTC()
				}
				if err := s.updateStateByKey(ctx, userID, scheduleStateKey(job), func(st *productScheduleUserState) {
					if strings.TrimSpace(st.ActivatedAt) == "" {
						st.ActivatedAt = activatedAt.Format(time.RFC3339)
					}
				}); err != nil {
					scheduleLogf("[PRODUCT-SCHEDULE] %s: cannot persist activation baseline for %s: %v", job.ID(), userID, err)
					continue
				}
				job.State.ActivatedAt = activatedAt.Format(time.RFC3339)
			}
			in := productschedule.Inputs{
				Now:                 now,
				ActivatedAt:         activatedAt,
				LastRun:             job.lastRun(),
				LastAttempt:         job.lastAttempt(),
				ConsecutiveFailures: job.State.ConsecutiveFailures,
				SinceInteractive:    productInteractionNever,
			}
			if s.sinceInteractive != nil {
				in.SinceInteractive = s.sinceInteractive(userID, job.Profile)
			}
			d := productschedule.Decide(job.Effective(), in)
			if d.Deferred {
				s.setDeferred(userID, job.ID(), d.Reason)
			} else {
				s.setDeferred(userID, job.ID(), "")
			}
			if !d.Run {
				if d.Deferred {
					scheduleLogf("[PRODUCT-SCHEDULE] %s deferring for %s: %s", job.ID(), userID, d.Reason)
				}
				continue
			}
			go func(job productScheduleJob, scheduledFor time.Time) {
				if _, err := s.Run(context.Background(), job, "cron", scheduledFor); err != nil && !errors.Is(err, productschedule.ErrAlreadyRunning) {
					scheduleLogf("[PRODUCT-SCHEDULE] %s failed for %s: %v", job.ID(), job.UserID, err)
				}
			}(job, d.ScheduledFor)
		}
	}
}

// Trigger runs a product schedule now for one user, ignoring its enabled flag.
func (s *ProductScheduleService) Trigger(ctx context.Context, userID, jobID string) (string, error) {
	job, err := s.Job(ctx, userID, jobID)
	if err != nil {
		return "", err
	}
	type result struct {
		sessionID string
		err       error
	}
	started := make(chan result, 1)
	go func() {
		sessionID, err := s.Run(context.Background(), job, "manual", time.Time{}, func(sessionID string) {
			started <- result{sessionID: sessionID}
		})
		select {
		case started <- result{sessionID: sessionID, err: err}:
		default:
		}
	}()
	r := <-started
	return r.sessionID, r.err
}

// ResetHistory clears an isolated schedule's own conversation: the next run
// opens a fresh session, and the transcript a "view check-in history" reader
// was showing is gone. Refused on a schedule that isn't Isolated (there is
// no separate conversation to clear — resetting would touch the profile's
// own chat) and while a run is in progress (rotating the session out from
// under a live turn).
func (s *ProductScheduleService) ResetHistory(ctx context.Context, userID, jobID string) error {
	job, err := s.Job(ctx, userID, jobID)
	if err != nil {
		return err
	}
	if !job.Schedule.Isolated {
		return fmt.Errorf("schedule %q does not run its own conversation", jobID)
	}
	key := userID + "\x1f" + jobID
	s.mu.Lock()
	_, busy := s.running[key]
	s.mu.Unlock()
	if busy {
		return productschedule.ErrAlreadyRunning
	}
	binding, err := resolveIsolatedScheduleBinding(ctx, userID, job.Profile)
	if err != nil {
		return err
	}
	_, err = defaultProductConversationRegistryStore().rotate(ctx, userID, job.Profile, binding)
	return err
}

// Running reports the live run for a job, if any.
func (s *ProductScheduleService) Running(userID, jobID string) (productScheduleRun, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.running[userID+"\x1f"+jobID]
	if !ok {
		return productScheduleRun{}, false
	}
	return *r, true
}

// Stop cancels the live run for a job.
func (s *ProductScheduleService) Stop(userID, jobID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.running[userID+"\x1f"+jobID]
	if !ok {
		return false
	}
	r.cancel()
	return true
}

// Run executes one schedule for one user: resolve the product conversation,
// record a run, send every message in turn, then record the outcome.
// onStarted, when given, is called once the session id is known.
func (s *ProductScheduleService) Run(ctx context.Context, job productScheduleJob, triggerSource string, scheduledFor time.Time, onStarted ...func(sessionID string)) (string, error) {
	return s.runWithOptions(ctx, job, triggerSource, scheduledFor, productScheduleRunOptions{}, onStarted...)
}

type productScheduleRunOptions struct {
	RunID          string
	Webhook        *WebhookRunMetadata
	FunctionCallID string
	// Detach claims the conversation and runs the turn in the background,
	// returning before it completes. Webhook deliveries use it; schedulers
	// run inline.
	Detach bool
	// AllowQueue accepts the delivery behind a live turn instead of failing
	// with ErrAlreadyRunning. The record stays queued until its turn starts.
	AllowQueue bool
}

// ErrProductRunQueued reports that a delivery was accepted behind a live turn.
var ErrProductRunQueued = errors.New("product schedules: conversation occupied, delivery queued")

// ErrProductQueueFull reports that a conversation queue has no room left.
var ErrProductQueueFull = errors.New("product schedules: conversation queue is full")

// maxProductConversationQueue bounds queued webhook deliveries per conversation.
const maxProductConversationQueue = 100

// productScheduleQueuedRun is one delivery waiting its turn in a conversation.
type productScheduleQueuedRun struct {
	job           productScheduleJob
	triggerSource string
	scheduledFor  time.Time
	options       productScheduleRunOptions
	onStarted     []func(sessionID string)
}

// conversationKeyForJob is the mutual-exclusion identity of the conversation a
// job runs in. Isolated automations own their conversation, so the job id is
// already unique; main-chat jobs of one project share one conversation.
func conversationKeyForJob(job productScheduleJob) string {
	if job.ProjectID != "" && !job.Schedule.Isolated {
		// A schedule set from a side chat queues with that chat, not the main one.
		return "conversation:" + strings.TrimSpace(job.Profile.ID) + ":" + firstNonEmptyTrimmed(scheduleChatKey(job), job.ProjectID)
	}
	return job.ID()
}

// popProductQueueLocked removes and returns the head of one conversation
// queue. The caller must hold s.mu.
func popProductQueueLocked(queued map[string][]productScheduleQueuedRun, convKey string) (productScheduleQueuedRun, bool) {
	items := queued[convKey]
	if len(items) == 0 {
		return productScheduleQueuedRun{}, false
	}
	head := items[0]
	if len(items) == 1 {
		delete(queued, convKey)
	} else {
		queued[convKey] = append([]productScheduleQueuedRun(nil), items[1:]...)
	}
	return head, true
}

func (s *ProductScheduleService) runWithOptions(ctx context.Context, job productScheduleJob, triggerSource string, scheduledFor time.Time, options productScheduleRunOptions, onStarted ...func(sessionID string)) (string, error) {
	if s.api == nil {
		return "", fmt.Errorf("product schedules: server not ready")
	}
	claim, err := s.claimAutomationRun(ctx, job, triggerSource, scheduledFor, options, onStarted)
	if err != nil {
		return "", err
	}
	if options.Detach {
		go s.executeAutomationRun(claim.runCtx, claim.cancel, job, triggerSource, scheduledFor, options, claim.run, claim.jobKey, claim.convKey, onStarted)
		return "", nil
	}
	return s.executeAutomationRun(claim.runCtx, claim.cancel, job, triggerSource, scheduledFor, options, claim.run, claim.jobKey, claim.convKey, onStarted)
}

// automationClaim is one reserved conversation turn.
type automationClaim struct {
	run     *productScheduleRun
	runCtx  context.Context
	cancel  context.CancelFunc
	jobKey  string
	convKey string
}

// claimAutomationRun reserves the conversation for one run, or queues the
// delivery behind the live turn when options allow it.
func (s *ProductScheduleService) claimAutomationRun(ctx context.Context, job productScheduleJob, triggerSource string, scheduledFor time.Time, options productScheduleRunOptions, onStarted []func(sessionID string)) (*automationClaim, error) {
	jobKey := job.UserID + "\x1f" + job.ID()
	convKey := job.UserID + "\x1f" + conversationKeyForJob(job)
	runCtx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	if s.conversations[convKey] || (s.heldConversation != nil && s.heldConversation(convKey)) {
		if !options.AllowQueue {
			s.mu.Unlock()
			cancel()
			return nil, productschedule.ErrAlreadyRunning
		}
		if len(s.queued[convKey]) >= maxProductConversationQueue {
			s.mu.Unlock()
			cancel()
			return nil, ErrProductQueueFull
		}
		if s.queued == nil {
			s.queued = map[string][]productScheduleQueuedRun{}
		}
		s.queued[convKey] = append(s.queued[convKey], productScheduleQueuedRun{job: job, triggerSource: triggerSource, scheduledFor: scheduledFor, options: options, onStarted: onStarted})
		s.mu.Unlock()
		cancel()
		return nil, ErrProductRunQueued
	}
	run := &productScheduleRun{StartedAt: time.Now().UTC(), cancel: cancel}
	if s.conversations == nil {
		s.conversations = map[string]bool{}
	}
	if s.running == nil {
		s.running = map[string]*productScheduleRun{}
	}
	s.conversations[convKey] = true
	s.running[jobKey] = run
	s.mu.Unlock()
	return &automationClaim{run: run, runCtx: runCtx, cancel: cancel, jobKey: jobKey, convKey: convKey}, nil
}

// finishAutomationRun releases one conversation turn and starts the next
// queued delivery, if any.
func (s *ProductScheduleService) finishAutomationRun(jobKey, convKey string) {
	s.mu.Lock()
	delete(s.running, jobKey)
	delete(s.conversations, convKey)
	next, ok := popProductQueueLocked(s.queued, convKey)
	if !ok {
		s.mu.Unlock()
		return
	}
	if s.conversations == nil {
		s.conversations = map[string]bool{}
	}
	if s.running == nil {
		s.running = map[string]*productScheduleRun{}
	}
	s.conversations[convKey] = true
	nextCtx, nextCancel := context.WithCancel(context.Background())
	nextRun := &productScheduleRun{StartedAt: time.Now().UTC(), cancel: nextCancel}
	nextJobKey := next.job.UserID + "\x1f" + next.job.ID()
	s.running[nextJobKey] = nextRun
	s.mu.Unlock()
	go s.executeAutomationRun(nextCtx, nextCancel, next.job, next.triggerSource, next.scheduledFor, next.options, nextRun, nextJobKey, convKey, next.onStarted)
}

// failAutomationSetupRun records a terminal error for a pre-claimed run when
// the worker fails before execution starts (unresolvable conversation
// binding or registry). Without this the accepted run stays queued with no
// worker: pollers wait until timeout instead of receiving the failure, and
// redelivery adopts the same stranded record. Only webhook deliveries
// pre-claim a record (options.RunID); cron runs claim nothing up front and
// keep their existing report-the-error behavior.
func (s *ProductScheduleService) failAutomationSetupRun(job productScheduleJob, options productScheduleRunOptions, setupErr error) {
	runID := strings.TrimSpace(options.RunID)
	if runID == "" || strings.TrimSpace(job.WorkspacePath) == "" {
		return
	}
	runsWorkspace := agentProfileRuntimeWorkspace(job.UserID, job.WorkspacePath)
	if job.Profile.ID == codeproduct.ProfileID && job.CodeCaller != nil {
		runsWorkspace = codePeerPrivateRunsWorkspace(job.UserID, job.WorkspacePath, job.ProjectID)
	}
	duration := int64(0)
	completion := ScheduleRunCompletion{Status: "error", Error: setupErr.Error(), DurationMs: &duration}
	if uerr := UpdateScheduleRunResult(context.Background(), runsWorkspace, runID, completion); uerr == nil {
		return
	} else if !strings.Contains(uerr.Error(), "not found") {
		scheduleLogf("[PRODUCT-SCHEDULE] %s: cannot record setup failure for %s: %v", job.ID(), job.UserID, uerr)
		return
	}
	// Retention trimmed the pre-claim between acceptance and worker start;
	// recreate the record already terminal so the delivery still polls.
	now := time.Now().UTC()
	if aerr := AppendScheduleRun(context.Background(), runsWorkspace, &ScheduleRunEntry{
		ID: runID, ScheduleID: job.ID(), TriggerSource: "webhook", Webhook: options.Webhook,
		Status: "error", Error: setupErr.Error(), StartedAt: now, CompletedAt: &now,
	}); aerr != nil {
		scheduleLogf("[PRODUCT-SCHEDULE] %s: cannot record setup failure for %s: %v", job.ID(), job.UserID, aerr)
	}
}

// executeAutomationRun runs one claimed automation turn to completion, then
// releases the conversation and starts the next queued delivery, if any.
func (s *ProductScheduleService) executeAutomationRun(runCtx context.Context, cancel context.CancelFunc, job productScheduleJob, triggerSource string, scheduledFor time.Time, options productScheduleRunOptions, run *productScheduleRun, jobKey, convKey string, onStarted []func(sessionID string)) (string, error) {
	if options.FunctionCallID != "" {
		runCtx = virtualtools.WithFeedbackOperation(runCtx, options.FunctionCallID)
	}
	defer func() {
		s.finishAutomationRun(jobKey, convKey)
		cancel()
	}()

	// One identity for the run record.
	runID := firstNonEmptyTrimmed(options.RunID, uuid.NewString())
	var binding productConversationBinding
	var bindErr error
	if routeErr := s.validateProjectCallConversation(runCtx, job); routeErr != nil {
		bindErr = routeErr
	} else if job.ProjectID != "" && job.Profile.ID == codeproduct.ProfileID && job.CodeCaller != nil && job.AutomationKind == "trigger" {
		// Revalidate private Code ownership even for a main-chat delivery.
		binding, bindErr = s.codeFunctionRunBinding(runCtx, job, job.ProjectTitle+" · "+job.Schedule.Name)
	} else if job.ProjectID != "" && job.Schedule.Isolated {
		kind := firstNonEmptyTrimmed(job.AutomationKind, "schedule")
		title := job.ProjectTitle + " · " + job.Schedule.Name
		binding, bindErr = resolveIsolatedProjectAutomationBinding(runCtx, job.UserID, job.Profile, job.ProjectID, kind, crewCallIsolatedKey(job.Schedule.ID, job.ConversationKey), title)
	} else if job.ProjectID != "" {
		// A schedule set from a side chat (tab) runs there while that chat
		// exists; otherwise, and for every other schedule, in the main chat.
		key := job.ProjectID
		if chatKey := scheduleChatKey(job); chatKey != "" && projectChatExists(runCtx, job.UserID, job.Profile.ID, job.ProjectID, chatKey) {
			key = chatKey
		}
		binding, bindErr = resolveProductConversationBinding(runCtx, job.UserID, job.Profile, key)
	} else if job.Schedule.Isolated {
		binding, bindErr = resolveIsolatedScheduleBinding(runCtx, job.UserID, job.Profile)
	} else {
		binding, bindErr = resolveProductConversationBinding(runCtx, job.UserID, job.Profile, "")
	}
	if bindErr != nil {
		setupErr := fmt.Errorf("resolve product conversation: %w", bindErr)
		s.failAutomationSetupRun(job, options, setupErr)
		return "", setupErr
	}
	conversation, err := defaultProductConversationRegistryStore().resolveOrCreate(runCtx, job.UserID, job.Profile, binding, "")
	if err != nil {
		setupErr := fmt.Errorf("open product conversation: %w", err)
		s.failAutomationSetupRun(job, options, setupErr)
		return "", setupErr
	}
	sessionID := conversation.SessionID
	s.mu.Lock()
	run.SessionID = sessionID
	s.mu.Unlock()
	for _, cb := range onStarted {
		if cb != nil {
			cb(sessionID)
		}
	}

	runsWorkspace := agentProfileRuntimeWorkspace(job.UserID, conversation.WorkspacePath)
	if job.Profile.ID == codeproduct.ProfileID && job.CodeCaller != nil {
		runsWorkspace = codePeerPrivateRunsWorkspace(job.UserID, job.WorkspacePath, job.ProjectID)
	}
	startedAt := time.Now().UTC()
	entry := &ScheduleRunEntry{
		ID:            runID,
		ScheduleID:    job.ID(),
		TriggerSource: triggerSource,
		Webhook:       options.Webhook,
		SessionID:     sessionID,
		Status:        "running",
		StartedAt:     startedAt,
	}
	if !scheduledFor.IsZero() {
		sf := scheduledFor.UTC()
		entry.ScheduledFor = &sf
	}
	if strings.TrimSpace(options.RunID) != "" {
		// Webhook deliveries pre-claim their record; move it to running.
		// Recreate it when retention already trimmed the pre-claim.
		if uerr := UpdateScheduleRun(runCtx, runsWorkspace, entry.ID, entry.Status, "", nil, "", sessionID); uerr != nil {
			if aerr := AppendScheduleRun(runCtx, runsWorkspace, entry); aerr != nil {
				scheduleLogf("[PRODUCT-SCHEDULE] %s: cannot record run for %s: %v", job.ID(), job.UserID, aerr)
			}
		}
	} else if err := AppendScheduleRun(runCtx, runsWorkspace, entry); err != nil {
		scheduleLogf("[PRODUCT-SCHEDULE] %s: cannot record run for %s: %v", job.ID(), job.UserID, err)
	}
	_ = s.updateStateByKey(runCtx, job.UserID, scheduleStateKey(job), func(st *productScheduleUserState) {
		st.LastStatus = "running"
		st.LastSessionID = sessionID
		st.LastError = ""
		st.LastAttemptAt = startedAt.Format(time.RFC3339)
	})

	// A person may set their own messages for a schedule (Brain: what Organize Brain should do, PLAT-618).
	messages := job.Effective().Messages
	scheduleLogf("[PRODUCT-SCHEDULE] 🚀 %s (%s) for user %s: %d message(s), session %s", job.ID(), job.Schedule.Name, job.UserID, len(messages), sessionID)
	var runErr error
	finalResponse := ""
	tokenUsage := &workflowtypes.CrewRunTokenUsage{}
	for i, message := range messages {
		if err := runCtx.Err(); err != nil {
			runErr = err
			break
		}
		req, err := queryRequestForAgentProfileChat(job.Profile, AgentProfileChatRequest{Message: message}, conversation)
		if err != nil {
			runErr = err
			break
		}
		reqMap, err := queryRequestToMap(req)
		if err != nil {
			runErr = err
			break
		}
		reqMap["triggered_by"] = firstNonEmptyTrimmed(triggerSource, "cron")
		applyCrewGuestCaller(reqMap, job.GuestCallerID)
		reqMap["triggered_by_label"] = automationTriggerLabel(firstNonEmptyTrimmed(triggerSource, "cron"), job.Schedule.Name)
		applyAutomationCostSource(reqMap, job, runID)
		reqMap["session_title"] = firstNonEmptyTrimmed(conversation.Title, job.Profile.Name)
		scheduleLogf("[PRODUCT-SCHEDULE] %s turn %d/%d for %s", job.ID(), i+1, len(messages), job.UserID)
		turnResult, err := s.api.startSessionInternalWithResult(runCtx, reqMap, sessionID, job.UserID, nil)
		if err != nil {
			runErr = fmt.Errorf("message %d/%d: %w", i+1, len(messages), err)
			break
		}
		if strings.TrimSpace(turnResult.FinalResponse) != "" {
			finalResponse = strings.TrimSpace(turnResult.FinalResponse)
		}
		accumulateAutomationTurnUsage(s.api.costLedger, tokenUsage, turnResult.QueryID)
	}

	status := "success"
	errMsg := ""
	if runErr != nil {
		status = "error"
		if errors.Is(runErr, context.Canceled) {
			status = "stopped"
		}
		errMsg = runErr.Error()
	}
	duration := time.Since(startedAt).Milliseconds()
	// One atomic write: status, response, and usage land together so a
	// poller never observes terminal success with an empty response.
	if uerr := UpdateScheduleRunResult(context.Background(), runsWorkspace, entry.ID, ScheduleRunCompletion{
		Status: status, Error: errMsg, DurationMs: &duration,
		SessionID: sessionID, FinalResponse: finalResponse, Usage: tokenUsage,
	}); uerr != nil {
		scheduleLogf("[PRODUCT-SCHEDULE] %s: cannot record run result: %v", job.ID(), uerr)
	}
	_ = s.updateStateByKey(context.Background(), job.UserID, scheduleStateKey(job), func(st *productScheduleUserState) {
		st.LastStatus = status
		st.LastError = errMsg
		st.LastDurationMs = &duration
		st.RunCount++
		if runErr == nil {
			st.LastRunAt = time.Now().UTC().Format(time.RFC3339)
			st.ConsecutiveFailures = 0
		} else {
			st.ConsecutiveFailures++
		}
	})
	if runErr != nil {
		scheduleLogf("[PRODUCT-SCHEDULE] ❌ %s for %s: %v", job.ID(), job.UserID, runErr)
		return sessionID, runErr
	}
	scheduleLogf("[PRODUCT-SCHEDULE] ✅ %s for %s completed in %dms", job.ID(), job.UserID, duration)
	return sessionID, nil
}

// applyAutomationCostSource records this run's turns, and their sub-agents,
// under cost scope "schedule" with the schedule's id and name, so a project's
// Cost Analysis separates them from the person's own chatting (PLAT-702).
func applyAutomationCostSource(reqMap map[string]interface{}, job productScheduleJob, runID string) {
	label := strings.TrimSpace(job.Schedule.Name)
	if job.AutomationKind == "trigger" {
		label = "Trigger: " + firstNonEmptyTrimmed(label, job.Schedule.ID)
	}
	reqMap["cost_source_id"] = job.ID()
	reqMap["cost_source_label"] = firstNonEmptyTrimmed(label, job.Schedule.ID)
	reqMap["cost_source_run_id"] = runID
}

func queryRequestToMap(req QueryRequest) (map[string]interface{}, error) {
	raw, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	out := map[string]interface{}{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// RunsWorkspace is where a job's schedule-runs.json lives for a user.
func (s *ProductScheduleService) RunsWorkspace(ctx context.Context, job productScheduleJob) (string, error) {
	if job.ProjectID != "" {
		return agentProfileRuntimeWorkspace(job.UserID, job.WorkspacePath), nil
	}
	binding, err := resolveProductConversationBinding(ctx, job.UserID, job.Profile, "")
	if err != nil {
		return "", err
	}
	return agentProfileRuntimeWorkspace(job.UserID, binding.WorkspacePath), nil
}

// jobResponse renders a product schedule in the shape the schedules UI reads.
func (s *ProductScheduleService) jobResponse(job productScheduleJob, runsWorkspace string) ScheduledJobResponse {
	sched := job.Effective()
	resp := ScheduledJobResponse{
		ID:                  job.ID(),
		ChatKey:             scheduleChatKey(job),
		Name:                sched.Name,
		Description:         sched.Description,
		EntityType:          "product",
		WorkspacePath:       runsWorkspace,
		WorkflowID:          firstNonEmptyTrimmed(job.ProjectID, job.Profile.ID),
		WorkflowLabel:       firstNonEmptyTrimmed(job.ProjectTitle, job.Profile.Name),
		Mode:                "workshop",
		Messages:            sched.Messages,
		ScheduleType:        "cron",
		CronExpression:      sched.CronExpression,
		Timezone:            sched.Timezone,
		Enabled:             sched.Enabled,
		LastSessionID:       job.State.LastSessionID,
		LastStatus:          job.State.LastStatus,
		LastError:           job.State.LastError,
		LastDurationMs:      job.State.LastDurationMs,
		RunCount:            job.State.RunCount,
		ConsecutiveFailures: job.State.ConsecutiveFailures,
		RunDestination:      runDestination(job.Schedule.Isolated),
		CadenceHours:        sched.CadenceHours,
		DeferredReason:      s.deferredReason(job.UserID, job.ID()),
	}
	if sched.CronExpression == "" && sched.CadenceHours > 0 {
		// The cadence form has no cron line; describe it so the UI shows something.
		resp.Description = strings.TrimSpace(resp.Description + fmt.Sprintf(" (every %dh)", sched.CadenceHours))
	}
	if last := job.lastRun(); !last.IsZero() {
		resp.LastRunAt = &last
	}
	if strings.TrimSpace(sched.RunAt) != "" {
		// A one-time schedule is shown as a calendar entry: its date and time in the schedule's timezone.
		resp.ScheduleType = "calendar"
		resp.Description = strings.TrimSpace(resp.Description + " (one time)")
		if at, err := sched.RunAtTime(); err == nil {
			loc := time.Local
			if zone, zoneErr := time.LoadLocation(strings.TrimSpace(sched.Timezone)); zoneErr == nil && strings.TrimSpace(sched.Timezone) != "" {
				loc = zone
			}
			local := at.In(loc)
			resp.CalendarItems = []CalendarScheduleItem{{Date: local.Format("2006-01-02"), Time: local.Format("15:04")}}
			if sched.Enabled && job.lastRun().IsZero() {
				next := at.UTC()
				resp.NextRunAt = &next
			}
		}
	} else if sched.Enabled {
		d := productschedule.Decide(sched, productschedule.Inputs{Now: time.Now(), ActivatedAt: job.activatedAt(), LastRun: job.lastRun(), SinceInteractive: 365 * 24 * time.Hour})
		if !d.ScheduledFor.IsZero() {
			next := d.ScheduledFor.UTC()
			resp.NextRunAt = &next
		} else if sched.CronExpression != "" {
			resp.NextRunAt = getNextRunTime(sched.CronExpression, sched.Timezone)
		}
	}
	if r, ok := s.Running(job.UserID, job.ID()); ok {
		resp.LastStatus = "running"
		resp.LastSessionID = r.SessionID
		started := r.StartedAt
		resp.LastRunAt = &started
	}
	return resp
}

// scheduleChatKey is the side chat a project schedule was set from, when it is
// one of this project's chats ("<projectId>:chat:<id>"); "" means the main chat.
func scheduleChatKey(job productScheduleJob) string {
	key := strings.TrimSpace(job.Schedule.ChatKey)
	project := strings.TrimSpace(job.ProjectID)
	chatID := strings.TrimPrefix(key, project+codeChatSideMarker)
	if key == "" || project == "" || !strings.HasPrefix(key, project+codeChatSideMarker) || chatID == "" || strings.Contains(chatID, ":") {
		return ""
	}
	return key
}

// projectChatExists reports whether that chat is still one of the project's
// chats in the owner's conversation registry.
func projectChatExists(ctx context.Context, userID, profileID, projectID, chatKey string) bool {
	chats, err := defaultProductConversationRegistryStore().projectChats(ctx, userID, profileID, projectID)
	if err != nil {
		return false
	}
	for _, chat := range chats {
		if chat.ConversationKey == chatKey {
			return true
		}
	}
	return false
}
