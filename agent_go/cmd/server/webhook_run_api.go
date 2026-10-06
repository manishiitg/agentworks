package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/manishiitg/coding-agent-loop/workspace/slots"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/mux"
	stepworkflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/schedulerstate"
)

var webhookFolderPattern = regexp.MustCompile(`^iteration-([0-9]+)-hook$`)

func webhookWorkspaceRoot(workspace string) (*os.Root, error) {
	if !strings.HasPrefix(workspace, "Workflow/") || path.Clean(workspace) != workspace || strings.ContainsAny(workspace, "\\\x00") {
		return nil, fmt.Errorf("invalid workflow path")
	}
	docs, err := os.OpenRoot(stepworkflow.GetPromptDocsRoot())
	if err != nil {
		return nil, err
	}
	defer docs.Close()
	return docs.OpenRoot(workspace)
}

// Exclusive mkdir is the final arbiter, including across concurrent processes.
func allocateWebhookRunFolder(workspace, runID string) (string, error) {
	lock := scheduleRunFileLock(workspace + "/run-folder-allocation")
	lock.Lock()
	defer lock.Unlock()
	root, err := webhookWorkspaceRoot(workspace)
	if err != nil {
		return "", err
	}
	defer root.Close()
	if err = root.MkdirAll("runs", 0700); err != nil {
		return "", err
	}
	entries, err := fs.ReadDir(root.FS(), "runs")
	if err != nil {
		return "", err
	}
	n := 0
	if raw, e := root.ReadFile(".webhook-sequence"); e == nil {
		if previous, e := strconv.Atoi(strings.TrimSpace(string(raw))); e == nil && previous > n {
			n = previous
		}
	}
	for _, entry := range entries {
		if m := webhookFolderPattern.FindStringSubmatch(entry.Name()); len(m) > 0 {
			raw, e := root.ReadFile("runs/" + entry.Name() + "/.webhook-run-id")
			if e == nil && string(raw) == runID {
				return entry.Name(), nil
			}
		}
		if m := numberedRunFolderPattern.FindStringSubmatch(entry.Name()); len(m) > 0 {
			i, parseErr := strconv.Atoi(m[1])
			if parseErr == nil && i > n {
				n = i
			}
		}
	}
	for {
		n++
		folder := fmt.Sprintf("iteration-%d-hook", n)
		err = root.Mkdir("runs/"+folder, 0700)
		if os.IsExist(err) {
			continue
		}
		if err != nil {
			return "", err
		}
		// With slots on, a step runs as the owner's slot account (a member of the host's shared group, which the setgid
		// parent folder passes on): the run folder must be group-writable or its code steps cannot write their outputs
		// (PLAT-502). The scheduled-run allocator does the same; this one was missed.
		if slots.Enabled() {
			if err := root.Chmod("runs/"+folder, 0o770|os.ModeSetgid); err != nil {
				return "", err
			}
		}
		f, e := root.OpenFile("runs/"+folder+"/.webhook-run-id", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			return "", e
		}
		_, e = f.WriteString(runID)
		closeErr := f.Close()
		if e != nil {
			return "", e
		}
		if closeErr != nil {
			return "", closeErr
		}
		if e := root.WriteFile(".webhook-sequence", []byte(strconv.Itoa(n)), 0600); e != nil {
			return "", e
		}
		return folder, nil
	}
}

type webhookAccessClaims struct {
	Trigger string `json:"trigger"`
	Run     string `json:"run"`
	File    string `json:"file,omitempty"`
	Scope   string `json:"scope"`
	jwt.RegisteredClaims
}

func webhookAccessToken(trigger, run, file string, ttl time.Duration) (string, error) {
	if err := ValidateConfiguredAuthSecret(); err != nil {
		return "", err
	}
	c := webhookAccessClaims{Trigger: trigger, Run: run, File: file, Scope: "webhook-run", RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(ttl)), Issuer: "agentworks-webhook"}}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString(GetAuthSecret())
}
func validWebhookAccess(token, trigger, run, file string) bool {
	c := &webhookAccessClaims{}
	_, err := jwt.ParseWithClaims(token, c, func(t *jwt.Token) (interface{}, error) { return GetAuthSecret(), nil }, jwt.WithValidMethods([]string{"HS256"}), jwt.WithIssuer("agentworks-webhook"), jwt.WithExpirationRequired())
	return err == nil && c.Scope == "webhook-run" && c.Trigger == trigger && c.Run == run && (c.File == "" || c.File == file)
}
func webhookStatusPath(trigger, run string) string {
	return "/api/hooks/workflow/" + trigger + "/runs/" + run
}

type webhookArtifact struct {
	Name        string     `json:"name"`
	Path        string     `json:"path"`
	Size        int64      `json:"size_bytes"`
	DownloadURL string     `json:"download_url,omitempty"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
}
type webhookStepOutput struct {
	StepID    string                 `json:"step_id"`
	Group     string                 `json:"group,omitempty"`
	Outputs   map[string]interface{} `json:"outputs"`
	Artifacts []webhookArtifact      `json:"artifacts"`
}
type webhookRunResult struct {
	Version          string                 `json:"version,omitempty"`
	ArtifactsExpired bool                   `json:"artifacts_expired,omitempty"`
	Progress         []webhookProgressEntry `json:"progress"`
	RunID            string                 `json:"run_id"`
	Status           string                 `json:"status"`
	Terminal         bool                   `json:"terminal"`
	RunFolder        string                 `json:"run_folder"`
	Error            string                 `json:"error,omitempty"`
	FinishedAt       *time.Time             `json:"finished_at,omitempty"`
	Steps            []webhookStepOutput    `json:"steps"`
	Result           json.RawMessage        `json:"result,omitempty"`
	Truncated        bool                   `json:"truncated,omitempty"`
}

// Output paths are limited to a run's step output directories. Logs, source code,
// environment files, symlinks and paths outside that run are never published.
func webhookOutputPath(p string) bool {
	if path.Clean(p) != p || strings.ContainsAny(p, "\\\x00") || path.IsAbs(p) {
		return false
	}
	parts := strings.Split(p, "/")
	if !(len(parts) >= 3 && parts[0] == "execution") && !(len(parts) >= 4 && parts[1] == "execution") {
		return false
	}
	for _, part := range parts {
		if strings.HasPrefix(part, ".") || part == "code" || part == "logs" {
			return false
		}
	}
	return true
}
func collectWebhookOutputs(root *os.Root) ([]webhookStepOutput, bool, error) {
	result := []webhookStepOutput{}
	indexes := map[string]int{}
	count, total := 0, 0
	truncated := false
	// A group-free delivery records progress at the run root. This also
	// disambiguates a Relay step called "execution" from a legacy group
	// with that same name, while old run folders remain readable.
	_, progressErr := root.Lstat("webhook_progress.json")
	groupFree := progressErr == nil
	err := fs.WalkDir(root.FS(), ".", func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if d.IsDir() {
			if p != "." && (strings.HasPrefix(d.Name(), ".") || d.Name() == "code" || d.Name() == "logs") {
				return fs.SkipDir
			}
			return nil
		}
		if !webhookOutputPath(p) {
			return nil
		}
		info, e := d.Info()
		if e != nil {
			return e
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		count++
		if count > 10000 {
			truncated = true
			return fs.SkipAll
		}
		parts := strings.Split(p, "/")
		group, stepIndex := "", 1
		if parts[0] != "execution" || !groupFree && len(parts) >= 4 && parts[1] == "execution" {
			group, stepIndex = parts[0], 2
		}
		key := group + "/" + parts[stepIndex]
		idx, ok := indexes[key]
		if !ok {
			idx = len(result)
			indexes[key] = idx
			result = append(result, webhookStepOutput{StepID: parts[stepIndex], Group: group, Outputs: map[string]interface{}{}, Artifacts: []webhookArtifact{}})
		}
		name := strings.Join(parts[stepIndex+1:], "/")
		result[idx].Artifacts = append(result[idx].Artifacts, webhookArtifact{Name: name, Path: p, Size: info.Size()})
		if info.Size() <= 128*1024 && total+int(info.Size()) <= 2*1024*1024 {
			ext := strings.ToLower(path.Ext(p))
			if ext == ".json" || ext == ".txt" || ext == ".md" || ext == ".xml" || ext == ".csv" {
				f, e := root.Open(p)
				if e != nil {
					return e
				}
				b, e := io.ReadAll(io.LimitReader(f, 128*1024+1))
				f.Close()
				if len(b) > 128*1024 {
					return nil
				}
				if e != nil {
					return e
				}
				total += len(b)
				var v interface{}
				if ext != ".json" || json.Unmarshal(b, &v) != nil {
					v = string(b)
				}
				result[idx].Outputs[name] = v
			}
		}
		return nil
	})
	return result, truncated, err
}
func (s *SchedulerService) authorizeWebhookRun(w http.ResponseWriter, r *http.Request) (*ScheduleSearchResult, schedulerstate.Run, bool) {
	vars := mux.Vars(r)
	found, e := findScheduleByIDAny(r.Context(), vars["id"])
	if e != nil || found == nil {
		http.NotFound(w, r)
		return nil, schedulerstate.Run{}, false
	}
	sched := found.Manifest.Schedules[found.Index]
	if sched.Webhook == nil || !sched.Enabled || sched.IsInternalTrigger() {
		http.NotFound(w, r)
		return nil, schedulerstate.Run{}, false
	}
	bearer := ""
	if strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		bearer = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	}
	token := r.URL.Query().Get("token")
	if token == "" {
		token = bearer
	}
	valid := validWebhookAccess(token, vars["id"], vars["run"], r.URL.Query().Get("path"))
	if !valid && bearer != "" {
		secret, err := decryptSecretValueWithAAD(sched.Webhook.EncryptedSecret, webhookAAD(found.Manifest.ID, sched.ID))
		valid = err == nil && subtle.ConstantTimeCompare([]byte(secret), []byte(bearer)) == 1
	}
	if !valid {
		http.Error(w, "invalid run credentials", 401)
		return nil, schedulerstate.Run{}, false
	}
	run, e := s.existingWebhookRun(r.Context(), vars["run"])
	if e != nil || !webhookRunMatchesSchedule(run, found.WorkspacePath, found.Manifest, sched) {
		http.NotFound(w, r)
		return nil, run, false
	}
	return found, run, true
}

// webhookRunMatchesSchedule reports whether a stored run belongs to the given
// trigger delivery scope. Both the public poll endpoint and internal dispatch
// use it so runs can never leak across triggers.
func webhookRunMatchesSchedule(run schedulerstate.Run, workspacePath string, manifest *WorkflowManifest, sched WorkflowSchedule) bool {
	scope, scopeID, _ := scheduleStateScope(buildScheduleContext(workspacePath, manifest, sched))
	return run.ScheduleID == sched.ID && run.ScopeType == scope && run.ScopeID == scopeID && run.TriggerSource == "webhook"
}
func openWebhookRunRoot(workspace string, run schedulerstate.Run) (*os.Root, error) {
	if !webhookFolderPattern.MatchString(run.RunFolder) {
		return nil, fmt.Errorf("run artifacts are not allocated")
	}
	root, e := webhookWorkspaceRoot(workspace)
	if e != nil {
		return nil, e
	}
	defer root.Close()
	child, e := root.OpenRoot("runs/" + run.RunFolder)
	if e != nil {
		return nil, e
	}
	id, e := child.ReadFile(".webhook-run-id")
	if e != nil || string(id) != run.RunID {
		child.Close()
		return nil, fmt.Errorf("run folder identity mismatch")
	}
	return child, nil
}
func (s *SchedulerService) pollWebhookRun(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	found, run, ok := s.authorizeWebhookRun(w, r)
	if !ok {
		return
	}
	result, err := readWebhookRunResult(found.WorkspacePath, run)
	if err != nil {
		switch {
		case errors.Is(err, errWebhookStoredResult):
			http.Error(w, "stored result unavailable", 503)
		case errors.Is(err, errWebhookPersistResult):
			http.Error(w, "cannot persist result", 503)
		default:
			http.Error(w, "run outputs unavailable", 503)
		}
		return
	}
	applyRelayResult(found.Manifest, &result, found.WorkspacePath, run)
	if err := signWebhookRunArtifacts(&result, run); err != nil {
		http.Error(w, "artifact signing unavailable", 503)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

var (
	errWebhookStoredResult  = errors.New("stored result unavailable")
	errWebhookRunOutputs    = errors.New("run outputs unavailable")
	errWebhookPersistResult = errors.New("cannot persist result")
)

// readWebhookRunResult assembles the pollable outcome of one workflow trigger
// run. The public poll endpoint and internal dispatch share it so Crew
// callers see the same steps, progress, and terminal snapshot.
func readWebhookRunResult(workspacePath string, run schedulerstate.Run) (webhookRunResult, error) {
	lock := scheduleRunFileLock("webhook-result:" + run.RunID)
	lock.Lock()
	defer lock.Unlock()
	result := webhookRunResult{RunID: run.RunID, Status: string(run.State), Terminal: run.CompletedAt != nil, RunFolder: run.RunFolder, Error: run.ErrorMessage, FinishedAt: run.CompletedAt, Steps: []webhookStepOutput{}}
	result.ArtifactsExpired = webhookArtifactsExpired(workspacePath, run.RunID)
	result.Progress = []webhookProgressEntry{}
	root, e := openWebhookRunRoot(workspacePath, run)
	if e == nil {
		defer root.Close()
		snapshot, readErr := root.ReadFile(".webhook-result.json")
		if result.Terminal && readErr == nil {
			if uErr := json.Unmarshal(snapshot, &result); uErr != nil {
				return webhookRunResult{}, fmt.Errorf("%w: %w", errWebhookStoredResult, uErr)
			}
		} else {
			result.Progress = collectWebhookProgress(root)
			result.Steps, result.Truncated, e = collectWebhookOutputs(root)
			if e != nil {
				return webhookRunResult{}, fmt.Errorf("%w: %w", errWebhookRunOutputs, e)
			}
			if result.Terminal {
				b, _ := json.Marshal(result)
				f, err := root.OpenFile(".webhook-result.tmp", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
				if err == nil {
					_, err = f.Write(b)
					closeErr := f.Close()
					if err == nil {
						err = closeErr
					}
					if err == nil {
						err = root.Rename(".webhook-result.tmp", ".webhook-result.json")
					}
				}
				if err != nil && !os.IsExist(err) {
					return webhookRunResult{}, fmt.Errorf("%w: %w", errWebhookPersistResult, err)
				}
			}
		}
	} else if run.RunFolder != "" && !result.ArtifactsExpired {
		return webhookRunResult{}, fmt.Errorf("%w: %w", errWebhookRunOutputs, e)
	}
	return result, nil
}

// applyRelayResult projects the selected step's JSON output onto the existing
// trigger result. The underlying workflow run and its logs remain unchanged.
func applyRelayResult(manifest *WorkflowManifest, result *webhookRunResult, workspacePath string, run schedulerstate.Run) {
	if manifest == nil || manifest.Kind != "relay" || result == nil || !result.Terminal || result.Error != "" || workflowRunStatusFailed(result.Status) {
		return
	}
	outputStepID := strings.TrimSpace(manifest.RelayOutputStepID)
	if workspacePath != "" && run.RunID != "" {
		if raw, exists, err := readFileFromWorkspace(context.Background(), webhookInputPath(workspacePath, run.RunID)); err == nil && exists {
			var delivery WorkflowWebhookDelivery
			var payload struct {
				OutputStepID string `json:"relay_output_step_id"`
			}
			if json.Unmarshal([]byte(raw), &delivery) == nil && json.Unmarshal(delivery.Payload, &payload) == nil && strings.TrimSpace(payload.OutputStepID) != "" {
				outputStepID = strings.TrimSpace(payload.OutputStepID)
			}
		}
	}
	if outputStepID == "" {
		result.Error = "Relay has no relay_output_step_id"
		result.Status = "failed"
		return
	}
	var selected interface{}
	found := false
	for _, step := range result.Steps {
		if step.StepID != outputStepID {
			continue
		}
		value, ok := step.Outputs["result.json"]
		if !ok {
			continue
		}
		if found {
			result.Error = "multiple Relay result.json outputs found; set relay_output_step_id"
			result.Status = "failed"
			return
		}
		selected, found = value, true
	}
	if !found {
		// Generic webhook snapshots stop inlining outputs after 2 MiB. A Relay's
		// selected result is its response contract, so read that one saved file
		// directly when it appears only in the artifact list.
		for _, step := range result.Steps {
			if step.StepID != outputStepID {
				continue
			}
			for _, artifact := range step.Artifacts {
				if artifact.Name != "result.json" || artifact.Size > 128*1024 {
					continue
				}
				root, err := openWebhookRunRoot(workspacePath, run)
				if err != nil {
					continue
				}
				data, err := root.ReadFile(artifact.Path)
				root.Close()
				if err == nil && len(data) <= 128*1024 && json.Unmarshal(data, &selected) == nil {
					found = true
				}
			}
		}
	}
	if !found {
		result.Error = "Relay completed without a result.json output"
		result.Status = "failed"
		return
	}
	encoded, err := json.Marshal(selected)
	if err != nil {
		result.Error = "Relay result.json could not be encoded"
		result.Status = "failed"
		return
	}
	result.Result = encoded
}

// signWebhookRunArtifacts attaches short-lived download URLs to run artifacts.
func signWebhookRunArtifacts(result *webhookRunResult, run schedulerstate.Run) error {
	for i := range result.Steps {
		for j := range result.Steps[i].Artifacts {
			a := &result.Steps[i].Artifacts[j]
			token, err := webhookAccessToken(run.ScheduleID, run.RunID, a.Path, 30*time.Minute)
			if err != nil {
				return err
			}
			expires := time.Now().Add(30 * time.Minute).UTC()
			a.ExpiresAt = &expires
			a.DownloadURL = webhookStatusPath(run.ScheduleID, run.RunID) + "/artifact?path=" + url.QueryEscape(a.Path) + "&token=" + url.QueryEscape(token)
		}
	}
	return nil
}

// readInternalWorkflowTriggerRun resolves the trigger from a known manifest
// and reads one of its runs for internal callers.
func (s *SchedulerService) readInternalWorkflowTriggerRun(ctx context.Context, workspacePath string, manifest *WorkflowManifest, triggerID, runID string, caller triggerCaller) (webhookRunResult, error) {
	sched, err := findInternalWorkflowTrigger(manifest, triggerID)
	if err != nil {
		return webhookRunResult{}, err
	}
	if sched.IsFunctionTrigger() {
		if !workflowFunctionCallerAllowed(sched.Function, caller) {
			return webhookRunResult{}, ErrInternalCallerMismatch
		}
	} else if !sched.Caller.matchesAnyPresented(caller) {
		return webhookRunResult{}, ErrInternalCallerMismatch
	}
	run, err := s.existingWebhookRun(ctx, runID)
	if err != nil {
		return webhookRunResult{}, ErrInternalTriggerRunGone
	}
	runWorkspace := workspacePath
	runManifest := manifest
	version := ""
	if manifest.Kind == "relay" && sched.IsFunctionTrigger() {
		content, exists, readErr := readFileFromWorkspace(ctx, webhookInputPath(run.ScopeID, runID))
		if readErr != nil || !exists {
			return webhookRunResult{}, ErrInternalTriggerRunGone
		}
		var delivery WorkflowWebhookDelivery
		var payload struct {
			Version string `json:"relay_version"`
		}
		if json.Unmarshal([]byte(content), &delivery) != nil || json.Unmarshal(delivery.Payload, &payload) != nil || payload.Version == "" {
			return webhookRunResult{}, ErrInternalTriggerRunGone
		}
		_, releaseWorkspace, releaseErr := readRelayRelease(ctx, workspacePath, payload.Version)
		if releaseErr != nil || run.ScopeID != releaseWorkspace {
			return webhookRunResult{}, ErrInternalTriggerRunGone
		}
		runWorkspace = releaseWorkspace
		runManifest, _, err = ReadWorkflowManifest(ctx, runWorkspace)
		if err != nil || runManifest == nil {
			return webhookRunResult{}, ErrInternalTriggerRunGone
		}
		version = payload.Version
	}
	runSched, err := findInternalWorkflowTrigger(runManifest, triggerID)
	if err != nil || !webhookRunMatchesSchedule(run, runWorkspace, runManifest, *runSched) {
		return webhookRunResult{}, ErrInternalTriggerRunGone
	}
	result, err := readWebhookRunResult(runWorkspace, run)
	if err != nil {
		return webhookRunResult{}, err
	}
	applyRelayResult(runManifest, &result, runWorkspace, run)
	result.Version = version
	if err := signWebhookRunArtifacts(&result, run); err != nil {
		return webhookRunResult{}, err
	}
	return result, nil
}
func (s *SchedulerService) downloadWebhookArtifact(w http.ResponseWriter, r *http.Request) {
	found, run, ok := s.authorizeWebhookRun(w, r)
	if !ok {
		return
	}
	if webhookArtifactsExpired(found.WorkspacePath, run.RunID) {
		http.Error(w, "run artifacts expired", http.StatusGone)
		return
	}
	p := r.URL.Query().Get("path")
	if !webhookOutputPath(p) {
		http.NotFound(w, r)
		return
	}
	root, e := openWebhookRunRoot(found.WorkspacePath, run)
	if e != nil {
		http.NotFound(w, r)
		return
	}
	defer root.Close()
	// Reject symlinks at every component, even links pointing elsewhere inside the run.
	parts := strings.Split(p, "/")
	for i := range parts {
		info, e := root.Lstat(strings.Join(parts[:i+1], "/"))
		if e != nil || info.Mode()&os.ModeSymlink != 0 {
			http.NotFound(w, r)
			return
		}
	}
	f, e := root.Open(p)
	if e != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filepath.Base(p)}))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "private, no-store")
	http.ServeContent(w, r, info.Name(), info.ModTime(), f)
}
