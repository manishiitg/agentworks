package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/viper"
)

const (
	defaultStaleWorkflowProcessAge = 2 * time.Hour
	processSweepInterval           = 5 * time.Minute
)

type ProcessOwner struct {
	Owner       string `json:"owner,omitempty"`
	WorkflowID  string `json:"workflow_id,omitempty"`
	RunID       string `json:"run_id,omitempty"`
	StepID      string `json:"step_id,omitempty"`
	ExecutionID string `json:"execution_id,omitempty"`
	SessionID   string `json:"session_id,omitempty"`
}

type ManagedProcess struct {
	PID        int          `json:"pid"`
	PGID       int          `json:"pgid,omitempty"`
	PPID       int          `json:"ppid,omitempty"`
	Command    string       `json:"command"`
	WorkingDir string       `json:"working_dir,omitempty"`
	StartedAt  time.Time    `json:"started_at"`
	TimeoutSec int          `json:"timeout_sec,omitempty"`
	Owner      ProcessOwner `json:"owner"`
	Status     string       `json:"status"`
	ExitCode   *int         `json:"exit_code,omitempty"`
}

type staleProcessCandidate struct {
	PID        int           `json:"pid"`
	PPID       int           `json:"ppid"`
	PGID       int           `json:"pgid,omitempty"`
	Elapsed    time.Duration `json:"elapsed"`
	Command    string        `json:"command"`
	Reason     string        `json:"reason"`
	WorkflowID string        `json:"workflow_id,omitempty"`
	RunID      string        `json:"run_id,omitempty"`
	StepID     string        `json:"step_id,omitempty"`
}

type psProcessSnapshot struct {
	PID     int
	PPID    int
	Elapsed time.Duration
	Command string
}

var managedProcesses = struct {
	sync.RWMutex
	byPID map[int]ManagedProcess
}{byPID: make(map[int]ManagedProcess)}

func registerShellProcess(cmd *exec.Cmd, owner ProcessOwner, command, workingDir string, timeoutSec int) ManagedProcess {
	record := ManagedProcess{
		Command:    command,
		WorkingDir: workingDir,
		StartedAt:  time.Now(),
		TimeoutSec: timeoutSec,
		Owner:      owner,
		Status:     "running",
	}
	if cmd != nil && cmd.Process != nil {
		record.PID = cmd.Process.Pid
		if pgid, err := syscall.Getpgid(record.PID); err == nil {
			record.PGID = pgid
		}
	}
	if record.PID <= 0 {
		return record
	}
	managedProcesses.Lock()
	managedProcesses.byPID[record.PID] = record
	managedProcesses.Unlock()
	if err := persistManagedProcess(record); err != nil {
		log.Printf("[PROCESS_SWEEPER] failed to persist process pid=%d: %v", record.PID, err)
	}
	return record
}

func finishShellProcess(pid int, status string, exitCode *int) {
	if pid <= 0 {
		return
	}
	managedProcesses.Lock()
	record, ok := managedProcesses.byPID[pid]
	if !ok {
		managedProcesses.Unlock()
		removePersistedProcessRecord(currentDocsDir(), pid)
		return
	}
	record.Status = status
	record.ExitCode = exitCode
	delete(managedProcesses.byPID, pid)
	managedProcesses.Unlock()
	removePersistedProcessRecord(currentDocsDir(), pid)
}

func persistManagedProcess(record ManagedProcess) error {
	if record.PID <= 0 || !isWorkflowProcess(record) {
		return nil
	}
	docsDir := currentDocsDir()
	if err := os.MkdirAll(processRegistryDir(docsDir), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(processRecordPath(docsDir, record.PID), data, 0600)
}

func removePersistedProcessRecord(docsDir string, pid int) {
	if pid <= 0 {
		return
	}
	if err := os.Remove(processRecordPath(docsDir, pid)); err != nil && !os.IsNotExist(err) {
		log.Printf("[PROCESS_SWEEPER] failed to remove process record pid=%d: %v", pid, err)
	}
}

func readPersistedProcessRecords(docsDir string) []ManagedProcess {
	entries, err := os.ReadDir(processRegistryDir(docsDir))
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("[PROCESS_SWEEPER] failed to read process registry: %v", err)
		}
		return nil
	}
	records := make([]ManagedProcess, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(processRegistryDir(docsDir), entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			log.Printf("[PROCESS_SWEEPER] failed to read process record %s: %v", path, err)
			continue
		}
		var record ManagedProcess
		if err := json.Unmarshal(data, &record); err != nil {
			log.Printf("[PROCESS_SWEEPER] failed to parse process record %s: %v", path, err)
			continue
		}
		if record.PID <= 0 {
			continue
		}
		records = append(records, record)
	}
	return records
}

func isWorkflowProcess(record ManagedProcess) bool {
	return record.Owner.Owner == "workflow" || record.Owner.WorkflowID != ""
}

func processRegistryDir(docsDir string) string {
	return filepath.Join(docsDir, ".runloop", "processes")
}

func processRecordPath(docsDir string, pid int) string {
	return filepath.Join(processRegistryDir(docsDir), fmt.Sprintf("%d.json", pid))
}

func ownerFromShellRequest(extraEnv map[string]string, workingDir, command string) ProcessOwner {
	owner := ProcessOwner{
		Owner:       strings.TrimSpace(extraEnv["RUNLOOP_OWNER"]),
		WorkflowID:  strings.TrimSpace(extraEnv["RUNLOOP_WORKFLOW_ID"]),
		RunID:       strings.TrimSpace(extraEnv["RUNLOOP_RUN_ID"]),
		StepID:      strings.TrimSpace(extraEnv["RUNLOOP_STEP_ID"]),
		ExecutionID: strings.TrimSpace(extraEnv["RUNLOOP_EXECUTION_ID"]),
		SessionID:   strings.TrimSpace(extraEnv["RUNLOOP_SESSION_ID"]),
	}
	for _, candidate := range []string{
		extraEnv["STEP_OUTPUT_DIR"],
		extraEnv["STEP_EXECUTION_DIR"],
		workingDir,
		command,
	} {
		if owner.WorkflowID != "" && owner.RunID != "" && owner.StepID != "" {
			break
		}
		inferred := inferWorkflowOwnerFromPath(candidate)
		if owner.WorkflowID == "" {
			owner.WorkflowID = inferred.WorkflowID
		}
		if owner.RunID == "" {
			owner.RunID = inferred.RunID
		}
		if owner.StepID == "" {
			owner.StepID = inferred.StepID
		}
	}
	if owner.Owner == "" && owner.WorkflowID != "" {
		owner.Owner = "workflow"
	}
	return owner
}

func inferWorkflowOwnerFromPath(raw string) ProcessOwner {
	slash := filepath.ToSlash(strings.TrimSpace(raw))
	if slash == "" {
		return ProcessOwner{}
	}
	parts := strings.Split(slash, "/")
	workflowIdx := -1
	for i, part := range parts {
		if part == "Workflow" {
			workflowIdx = i
			break
		}
	}
	if workflowIdx < 0 || workflowIdx+1 >= len(parts) {
		return ProcessOwner{}
	}
	owner := ProcessOwner{Owner: "workflow", WorkflowID: parts[workflowIdx+1]}
	runsIdx := -1
	for i := workflowIdx + 2; i < len(parts); i++ {
		if parts[i] == "runs" {
			runsIdx = i
			break
		}
	}
	if runsIdx < 0 {
		return owner
	}
	executionIdx := -1
	for i := runsIdx + 1; i < len(parts); i++ {
		if parts[i] == "execution" {
			executionIdx = i
			break
		}
	}
	if executionIdx < 0 {
		return owner
	}
	if executionIdx > runsIdx+1 {
		owner.RunID = strings.Join(parts[runsIdx+1:executionIdx], "/")
	}
	if executionIdx+1 < len(parts) {
		owner.StepID = parts[executionIdx+1]
	}
	return owner
}

func StartWorkflowProcessSweeper(docsDir string) {
	go func() {
		runWorkflowProcessSweep(docsDir)
		ticker := time.NewTicker(processSweepInterval)
		defer ticker.Stop()
		for range ticker.C {
			runWorkflowProcessSweep(docsDir)
		}
	}()
}

func runWorkflowProcessSweep(docsDir string) {
	killed, err := cleanupStaleWorkflowProcesses(docsDir, staleWorkflowProcessAge())
	if err != nil {
		log.Printf("[PROCESS_SWEEPER] cleanup error: %v", err)
		return
	}
	if len(killed) > 0 {
		log.Printf("[PROCESS_SWEEPER] killed %d stale workflow process(es)", len(killed))
	}
}

func cleanupStaleWorkflowProcesses(docsDir string, threshold time.Duration) ([]staleProcessCandidate, error) {
	candidates, err := findStaleWorkflowProcesses(docsDir, threshold)
	if err != nil {
		return nil, err
	}
	killed := make([]staleProcessCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if err := terminateProcessGroup(candidate.PID); err == nil {
			killed = append(killed, candidate)
		} else {
			log.Printf("[PROCESS_SWEEPER] failed to kill pid=%d: %v", candidate.PID, err)
		}
	}
	return killed, nil
}

func findStaleWorkflowProcesses(docsDir string, threshold time.Duration) ([]staleProcessCandidate, error) {
	if threshold <= 0 {
		threshold = defaultStaleWorkflowProcessAge
	}
	processes, err := snapshotPSProcesses()
	if err != nil {
		return nil, err
	}
	absDocsDir, _ := filepath.Abs(docsDir)
	needle := filepath.ToSlash(absDocsDir) + "/Workflow/"
	candidates := make([]staleProcessCandidate, 0)
	seen := make(map[int]bool)

	for _, record := range readPersistedProcessRecords(docsDir) {
		if !isWorkflowProcess(record) || record.PID == os.Getpid() {
			continue
		}
		process, ok := processes[record.PID]
		if !ok {
			removePersistedProcessRecord(docsDir, record.PID)
			continue
		}
		if process.Elapsed < threshold {
			continue
		}
		if processLooksReused(record, process.Elapsed) {
			removePersistedProcessRecord(docsDir, record.PID)
			continue
		}
		owner := record.Owner
		candidates = append(candidates, staleProcessCandidate{
			PID:        record.PID,
			PPID:       process.PPID,
			PGID:       record.PGID,
			Elapsed:    process.Elapsed,
			Command:    process.Command,
			Reason:     fmt.Sprintf("registered workflow process older than %s", threshold),
			WorkflowID: owner.WorkflowID,
			RunID:      owner.RunID,
			StepID:     owner.StepID,
		})
		seen[record.PID] = true
	}

	for _, process := range processes {
		if seen[process.PID] || process.PPID != 1 || process.Elapsed < threshold {
			continue
		}
		commandSlash := filepath.ToSlash(process.Command)
		if !strings.Contains(commandSlash, needle) ||
			!strings.Contains(commandSlash, "/runs/") ||
			!strings.Contains(commandSlash, "/execution/") {
			continue
		}
		if process.PID == os.Getpid() {
			continue
		}
		owner := inferWorkflowOwnerFromPath(commandSlash)
		candidates = append(candidates, staleProcessCandidate{
			PID:        process.PID,
			PPID:       process.PPID,
			Elapsed:    process.Elapsed,
			Command:    process.Command,
			Reason:     fmt.Sprintf("orphaned workflow-run process older than %s", threshold),
			WorkflowID: owner.WorkflowID,
			RunID:      owner.RunID,
			StepID:     owner.StepID,
		})
	}
	return candidates, nil
}

func snapshotPSProcesses() (map[int]psProcessSnapshot, error) {
	out, err := exec.Command("ps", "-Ao", "pid=,ppid=,etime=,command=").Output()
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(out), "\n")
	processes := make(map[int]psProcessSnapshot, len(lines))
	for _, line := range lines {
		pid, ppid, elapsed, command, ok := parsePSLine(line)
		if !ok {
			continue
		}
		processes[pid] = psProcessSnapshot{
			PID:     pid,
			PPID:    ppid,
			Elapsed: elapsed,
			Command: command,
		}
	}
	return processes, nil
}

func processLooksReused(record ManagedProcess, elapsed time.Duration) bool {
	if record.StartedAt.IsZero() {
		return false
	}
	recordAge := time.Since(record.StartedAt)
	return elapsed+5*time.Minute < recordAge
}

func parsePSLine(line string) (pid int, ppid int, elapsed time.Duration, command string, ok bool) {
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) < 4 {
		return 0, 0, 0, "", false
	}
	pid, err := strconv.Atoi(fields[0])
	if err != nil {
		return 0, 0, 0, "", false
	}
	ppid, err = strconv.Atoi(fields[1])
	if err != nil {
		return 0, 0, 0, "", false
	}
	elapsed, err = parsePSElapsed(fields[2])
	if err != nil {
		return 0, 0, 0, "", false
	}
	return pid, ppid, elapsed, strings.Join(fields[3:], " "), true
}

func parsePSElapsed(value string) (time.Duration, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, fmt.Errorf("empty elapsed")
	}
	days := 0
	if before, after, found := strings.Cut(value, "-"); found {
		parsedDays, err := strconv.Atoi(before)
		if err != nil {
			return 0, err
		}
		days = parsedDays
		value = after
	}
	parts := strings.Split(value, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, fmt.Errorf("invalid elapsed %q", value)
	}
	toInt := func(s string) (int, error) { return strconv.Atoi(strings.TrimSpace(s)) }
	hours := 0
	minutes := 0
	seconds := 0
	var err error
	if len(parts) == 3 {
		hours, err = toInt(parts[0])
		if err != nil {
			return 0, err
		}
		minutes, err = toInt(parts[1])
		if err != nil {
			return 0, err
		}
		seconds, err = toInt(parts[2])
		if err != nil {
			return 0, err
		}
	} else {
		minutes, err = toInt(parts[0])
		if err != nil {
			return 0, err
		}
		seconds, err = toInt(parts[1])
		if err != nil {
			return 0, err
		}
	}
	return time.Duration(days)*24*time.Hour +
		time.Duration(hours)*time.Hour +
		time.Duration(minutes)*time.Minute +
		time.Duration(seconds)*time.Second, nil
}

func terminateProcessGroup(pid int) error {
	if pid <= 0 {
		return fmt.Errorf("invalid pid %d", pid)
	}
	if pgid, err := syscall.Getpgid(pid); err == nil && pgid > 0 {
		if currentPGID, currentErr := syscall.Getpgid(os.Getpid()); currentErr != nil || pgid != currentPGID {
			_ = syscall.Kill(-pgid, syscall.SIGTERM)
			time.Sleep(750 * time.Millisecond)
			if !processExists(pid) {
				return nil
			}
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
			return nil
		}
	}
	_ = syscall.Kill(pid, syscall.SIGTERM)
	time.Sleep(750 * time.Millisecond)
	if processExists(pid) {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
	return nil
}

func processExists(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil
}

func staleWorkflowProcessAge() time.Duration {
	raw := strings.TrimSpace(os.Getenv("RUNLOOP_STALE_WORKFLOW_PROCESS_AGE"))
	if raw == "" {
		return defaultStaleWorkflowProcessAge
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return defaultStaleWorkflowProcessAge
	}
	return d
}

func currentDocsDir() string {
	if v := strings.TrimSpace(viper.GetString("docs-dir")); v != "" {
		return v
	}
	if v := strings.TrimSpace(os.Getenv("WORKSPACE_DOCS_DIR")); v != "" {
		return v
	}
	return "."
}
