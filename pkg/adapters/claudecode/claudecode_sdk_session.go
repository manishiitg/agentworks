package claudecode

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/manishiitg/multi-llm-provider-go/pkg/commandlabel"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/interfaces"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/internal/instructions"
)

// A persistent Claude SDK chat parks its existing query after every answer,
// not only while Claude's own tasks run (PLAT-746-941). The next unchanged
// launch continues that query. Task-free queries have a short idle bound and
// LRU cap; active background work keeps the existing wake/adoption path.

// SDKBackgroundTask is one background task Claude started itself that is still running.
type SDKBackgroundTask struct {
	TaskID      string    `json:"task_id"`
	Description string    `json:"description,omitempty"`
	Command     string    `json:"command,omitempty"`
	StartedAt   time.Time `json:"started_at"`
}

// SDKHelperTask is lifecycle evidence from this query's native Agent tasks.
// No child prompt or tool output is copied; shell commands use the shared short redacted label.
type SDKHelperTask struct {
	HelperType        string `json:"helper_type,omitempty"`
	HelperDescription string `json:"helper_description,omitempty"`
	HelperMode        string `json:"helper_mode,omitempty"`
	CurrentStep       string `json:"current_step,omitempty"`
	Command           string `json:"command,omitempty"`
	BackgroundMode    string `json:"background_mode,omitempty"`
	Kind              string `json:"kind,omitempty"`
	TotalTokens       *int64 `json:"total_tokens,omitempty"`
	DurationMS        int64  `json:"duration_ms,omitempty"`
	UpdatedAt         int64  `json:"updated_at,omitempty"`
	TaskID            string `json:"task_id"`
	RunID             string `json:"run_id"`
	ToolUseID         string `json:"tool_use_id"`
	Description       string `json:"description"`
	StartedAt         int64  `json:"started_at"`
	State             string `json:"state"`
}

type SDKHelperUpdate struct {
	Owner           string
	NativeSessionID string
	Task            SDKHelperTask
}

var claudeSDKHelperListener atomic.Pointer[func(SDKHelperUpdate)]

// SetSDKHelperListener observes native helpers on their exact parent chat.
func SetSDKHelperListener(listener func(SDKHelperUpdate)) {
	if listener == nil {
		claudeSDKHelperListener.Store(nil)
	} else {
		claudeSDKHelperListener.Store(&listener)
	}
}

func (s *claudeSDKSession) observeHelper(task SDKHelperTask) {
	task.Command = claudeBackgroundCommandLabel(task.Command)
	task.HelperType = commandlabel.Label(task.HelperType)
	task.HelperDescription = commandlabel.Label(task.HelperDescription)
	task.CurrentStep = commandlabel.Label(task.CurrentStep)
	if task.TaskID == "" {
		return
	}
	switch task.State {
	case "started", "finished", "failed", "interrupted", "progress":
	default:
		return
	}
	s.mu.Lock()
	if s.helpers == nil {
		s.helpers = map[string]SDKHelperTask{}
	}
	previous, exists := s.helpers[task.TaskID]
	if exists && previous.RunID != "" && task.RunID != "" && task.RunID < previous.RunID {
		s.mu.Unlock()
		return
	}
	if task.State == "progress" && (!exists || previous.RunID != task.RunID || previous.State != "started") {
		s.mu.Unlock()
		return
	}
	if exists && previous.RunID == task.RunID && (previous.State == task.State || previous.State != "started") {
		s.mu.Unlock()
		return
	}
	if exists && previous.RunID == task.RunID {
		if task.StartedAt == 0 {
			task.StartedAt = previous.StartedAt
		}
		if task.TotalTokens == nil {
			task.TotalTokens = previous.TotalTokens
		}
		if task.HelperType == "" {
			task.HelperType = previous.HelperType
		}
		if task.HelperDescription == "" {
			task.HelperDescription = previous.HelperDescription
		}
		if task.HelperMode == "" {
			task.HelperMode = previous.HelperMode
		}
		if task.CurrentStep == "" {
			task.CurrentStep = previous.CurrentStep
		}
		if task.Kind == "" {
			task.Kind = previous.Kind
		}
	}
	saved := task
	if saved.State == "progress" {
		saved.State = "started"
	}
	s.helpers[task.TaskID] = saved
	nativeID := s.nativeID
	s.mu.Unlock()
	if task.State != "progress" {
		s.logf("[SDK_HELPER] session=%s task=%s state=%s", s.owner, task.TaskID, task.State)
	}
	if listener := claudeSDKHelperListener.Load(); listener != nil {
		(*listener)(SDKHelperUpdate{Owner: s.owner, NativeSessionID: nativeID, Task: task})
	}
}

func (s *claudeSDKSession) interruptHelpers() {
	s.mu.Lock()
	var tasks []SDKHelperTask
	for _, task := range s.helpers {
		if task.State == "started" {
			tasks = append(tasks, task)
		}
	}
	s.mu.Unlock()
	for _, task := range tasks {
		task.State = "interrupted"
		task.UpdatedAt = time.Now().UnixMilli()
		s.observeHelper(task)
	}
}

// SDKBackgroundUpdate is the full list of a chat's running background tasks.
// Closed names why a kept session ended ("finished", "stopped", "idle",
// "replaced"); the list is then empty. IdleLimit is the idle bound in use.
// Wake: Claude started a turn on its own in the kept session (its background
// work finished); the host shows it by starting a turn that adopts it
// (llmtypes.WithCodingProviderAdoptWake) or closes the session.
type SDKBackgroundUpdate struct {
	NativeSessionID string
	WakeID          uint64
	Owner           string
	Tasks           []SDKBackgroundTask
	Closed          string
	IdleLimit       time.Duration
	Wake            bool
}

var claudeSDKBackgroundListener atomic.Pointer[func(SDKBackgroundUpdate)]

// SetSDKBackgroundListener receives every change of a chat's background task
// list, during turns and between them. The host shows it; nothing is decided there.
func SetSDKBackgroundListener(listener func(SDKBackgroundUpdate)) {
	if listener == nil {
		claudeSDKBackgroundListener.Store(nil)
		return
	}
	claudeSDKBackgroundListener.Store(&listener)
}

func notifyClaudeSDKBackground(update SDKBackgroundUpdate) {
	if update.Owner == "" {
		return
	}
	update.IdleLimit = persistentInteractiveIdleTimeout()
	if listener := claudeSDKBackgroundListener.Load(); listener != nil {
		(*listener)(update)
	}
}

// claudeSDKTaskOutput is live output of one of Claude's own background
// commands, which the bridge follows in the task's output file (#3131).
type claudeSDKTaskOutput struct {
	TaskID    string `json:"task_id"`
	ToolUseID string `json:"tool_use_id"`
	Text      string `json:"text"`
	Dropped   bool   `json:"dropped"`
	Done      bool   `json:"done"`
}

// emitClaudeTaskOutput hands the output to the host, during a turn and
// between turns alike. Claude's own background tasks can be stopped one by one
// (StopClaudeSDKBackgroundTask), so a running one says so.
func emitClaudeTaskOutput(owner string, output *claudeSDKTaskOutput) {
	if output == nil || owner == "" || output.TaskID == "" || output.ToolUseID == "" {
		return
	}
	llmtypes.EmitBackgroundOutput(llmtypes.BackgroundOutput{Provider: "claude-code", Owner: owner, ToolCallID: output.ToolUseID, TaskID: output.TaskID, Text: output.Text, Dropped: output.Dropped, Done: output.Done, CanStop: !output.Done})
}

type claudeSDKWireTask struct {
	TaskID      string `json:"task_id"`
	Description string `json:"description"`
	Command     string `json:"command"`
	StartedAt   int64  `json:"started_at"`
}

func claudeBackgroundCommandLabel(command string) string { return commandlabel.Label(command) }

func claudeSDKTasksFromWire(wire []claudeSDKWireTask) []SDKBackgroundTask {
	tasks := make([]SDKBackgroundTask, 0, len(wire))
	for _, task := range wire {
		if strings.TrimSpace(task.TaskID) == "" {
			continue
		}
		started := time.Now()
		if task.StartedAt > 0 {
			started = time.UnixMilli(task.StartedAt)
		}
		tasks = append(tasks, SDKBackgroundTask{TaskID: task.TaskID, Description: strings.TrimSpace(task.Description), Command: claudeBackgroundCommandLabel(task.Command), StartedAt: started})
	}
	return tasks
}

// claudeSDKLaunch describes what a kept session was started with, one digest
// per named part: model, instructions, permission mode, process environment
// (account and secrets), native questions, confinement, runtime folder and
// each launch option except the resume id. Only digests are kept, never
// another copy of credentials. The conversation is compared on its own.
type claudeSDKLaunch map[string][32]byte

func claudeSDKLaunchParts(model, systemPrompt, launchExtra, permissionMode string, environment []string, opts *llmtypes.CallOptions, identity ...string) claudeSDKLaunch {
	parts := claudeSDKLaunch{}
	add := func(name string, value any) {
		raw, err := json.Marshal(value)
		if err != nil {
			// Not comparable by content: compare its type only.
			raw = []byte(fmt.Sprintf("%T", value))
		}
		parts[name] = sha256.Sum256(raw)
	}
	// Model/effort/permission use acknowledged native setters. Current trusted
	// instructions (including private skills) use the existing resume carrier.
	// Empty instructions still force a restart rather than retaining old rules.
	add("instructions_empty", strings.TrimSpace(systemPrompt+launchExtra) == "")
	effectiveEnv := map[string]string{}
	for _, entry := range environment {
		if key, value, ok := strings.Cut(entry, "="); ok {
			effectiveEnv[key] = value
		}
	}
	add("environment", effectiveEnv)
	for i, key := range []string{"binary", "sdk", "working_dir"} {
		if i < len(identity) {
			add(key, identity[i])
		}
	}
	// Whether question cards may be shown is not part of the process: the
	// permission mode is set per turn and each permission request is answered
	// on its own turn, so a turn started by the server (a restart resume, an
	// agent message) and the person's next turn share one Claude process.
	if opts != nil {
		add("security", opts.CLISecurity)
		add("runtime_dir", opts.CLIRuntimeDir)
		if opts.Metadata != nil {
			for key, value := range opts.Metadata.Custom {
				switch key {
				case MetadataKeyResumeSessionID, MetadataKeyEffort, MetadataKeyPermissionMode, MetadataKeyDangerouslySkipPermissions:
					continue // Native conversation fence or acknowledged turn controls.
				case llmtypes.CodingProviderLaunchSystemPromptMetadataKey:
					continue // A copy of the instructions above, which the resume carrier refreshes.
				case llmtypes.AttachedSkillsMetadataKey:
					// Skills are rewritten into the skills folder before every turn
					// and Claude Code reloads that folder while it runs, so a skill
					// change reloads on the kept process instead of relaunching it
					// (decision (1), #2886).
					continue
				}
				// Compare JSON config contents, not formatting or map key order.
				if key == MetadataKeyMCPConfig || key == MetadataKeySettings {
					if raw, ok := value.(string); ok {
						var decoded any
						if json.Unmarshal([]byte(raw), &decoded) == nil {
							value = decoded
						}
					}
				}
				// Unknown/tool-policy options remain restart boundaries: never
				// silently reuse an old tool grant or MCP authentication context.
				add("option:"+key, value)
			}
		}
	}
	return parts
}

// Compare installed program identity without reading a large executable each turn.
func claudeSDKProgramIdentity(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s:%d:%d", resolved, info.Size(), info.ModTime().UnixNano()), nil
}

// changed names the parts that differ (names only, never values).
func (kept claudeSDKLaunch) changed(wanted claudeSDKLaunch) []string {
	var names []string
	for name, digest := range wanted {
		if old, ok := kept[name]; !ok || old != digest {
			names = append(names, name)
		}
	}
	for name := range kept {
		if _, ok := wanted[name]; !ok {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// claudeSDKReuseDecision says whether a kept session may take a turn, and why not.
func claudeSDKReuseDecision(kept, wanted claudeSDKLaunch, keptSessionID, resumeSessionID string) (bool, string) {
	if changed := kept.changed(wanted); len(changed) > 0 {
		return false, "launch_changed:" + strings.Join(changed, ",")
	}
	switch {
	case strings.TrimSpace(resumeSessionID) == "":
		return false, "new_conversation"
	case strings.TrimSpace(resumeSessionID) != strings.TrimSpace(keptSessionID):
		return false, "other_conversation"
	}
	return true, ""
}

type claudeSDKInput struct {
	mu sync.Mutex
	w  io.WriteCloser
}

func (in *claudeSDKInput) write(value any) error {
	in.mu.Lock()
	defer in.mu.Unlock()
	if streaming, ok := value.(claudeSDKStreamingParams); ok {
		if err := streaming.writeJSON(in.w); err != nil {
			return err
		}
		_, err := io.WriteString(in.w, "\n")
		return err
	}
	return json.NewEncoder(in.w).Encode(value)
}

type claudeLockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *claudeLockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *claudeLockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// claudeLineReader turns a process's stdout into lines. The channel closes at
// EOF; err is valid after done closes.
type claudeLineReader struct {
	lines chan string
	done  chan struct{}
	err   error
}

func startClaudeLineReader(stdout io.Reader) *claudeLineReader {
	r := &claudeLineReader{lines: make(chan string, 256), done: make(chan struct{})}
	go func() {
		defer close(r.done)
		defer close(r.lines)
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 1024*1024), 8*1024*1024)
		for scanner.Scan() {
			r.lines <- scanner.Text()
		}
		r.err = scanner.Err()
	}()
	return r
}

type claudeSDKSession struct {
	nextPrompt llmtypes.NextPromptScope
	owner      string
	launch     claudeSDKLaunch
	cmd        *exec.Cmd
	input      *claudeSDKInput
	reader     *claudeLineReader
	stderr     *claudeLockedBuffer
	exited     chan struct{}
	waitErr    error // valid after exited closes
	kill       context.CancelFunc
	logger     interfaces.Logger
	// process logs and counts this process's launch, reuses and exit (#2886).
	process *llmtypes.AgentProcess

	mu        sync.Mutex
	nativeID  string
	tasks     []SDKBackgroundTask
	helpers   map[string]SDKHelperTask
	parked    bool
	warm      bool // An ordinary persistent chat, kept even without tasks.
	lastUsed  time.Time
	closing   bool
	idle      *time.Timer
	drainStop chan struct{}
	drainDone chan struct{}
	// wakeGrace closes a session whose tasks all finished if Claude does not
	// start its own turn soon; wakeFirst is the first line of that turn, held
	// for the turn that adopts it.
	wakeGrace *time.Timer
	wakeHold  *time.Timer
	wakeFirst string
	wakeID    uint64
	// wakeDismissed: the chat does not show Claude's current own turn; its
	// output is read and dropped until its result.
	wakeDismissed bool
	// wakeAt: Claude scheduled its own later turn (ScheduleWakeup). No task
	// reports it, so it alone keeps a warm query alive until then (#3128).
	wakeAt time.Time
	// instructions: what this process holds of the AgentWorks instructions.
	instructions instructions.Tracker
}

// claudeSDKScheduledWakeMargin is how long after its scheduled time a
// ScheduleWakeup turn is still awaited. Claude fires it at most a minute
// around the minute-rounded time it reports; the rest is slack.
var claudeSDKScheduledWakeMargin = 2 * time.Minute

// claudeSDKBackgroundWaitPoll is how often a waiting turn checks whether the
// chat's background work has ended.
var claudeSDKBackgroundWaitPoll = 2 * time.Second

// waitForClaudeBackgroundWork holds a turn that needs a new Claude process
// until the kept process has no background commands or agents left, so the
// relaunch stops nothing. The chat shows one line while it waits; the turn's
// own Stop (its context) ends the wait.
func waitForClaudeBackgroundWork(ctx context.Context, owner string, stream chan<- llmtypes.StreamChunk, logger interface{ Infof(string, ...any) }) error {
	running := func() int {
		claudeSDKKept.Lock()
		s := claudeSDKKept.sessions[owner]
		claudeSDKKept.Unlock()
		if s == nil {
			return 0
		}
		return s.taskCount()
	}
	if running() == 0 {
		return nil
	}
	started := time.Now()
	if logger != nil {
		logger.Infof("[CLAUDE_WARM] session=%s action=process_restart_waiting background_tasks=%d", owner, running())
	}
	if stream != nil {
		select {
		case stream <- llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeReasoning, Content: "Waiting for background work in this chat to finish before replying. To reply now, stop it from the background work list.", Metadata: map[string]any{"background_wait": true}}:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	ticker := time.NewTicker(claudeSDKBackgroundWaitPoll)
	defer ticker.Stop()
	for running() > 0 {
		select {
		case <-ctx.Done():
			if logger != nil {
				logger.Infof("[CLAUDE_WARM] session=%s action=process_restart_wait_stopped waited_s=%d", owner, int(time.Since(started).Seconds()))
			}
			return ctx.Err()
		case <-ticker.C:
		}
	}
	if logger != nil {
		logger.Infof("[CLAUDE_WARM] session=%s action=process_restart_wait_done waited_s=%d", owner, int(time.Since(started).Seconds()))
	}
	return nil
}

// ErrClaudeBackgroundWorkBlocksRelaunch: this turn needs a new Claude process
// (its account, tools, MCP servers, environment or program changed), and
// starting one would end the background commands and agents Claude is still
// running in this chat. The turn waits for that work (waitForClaudeBackgroundWork)
// and fails with this only when it is stopped while waiting (#3128).
var ErrClaudeBackgroundWorkBlocksRelaunch = errors.New("claude-code: Claude is still running background commands or agents in this chat, and this change (account, tools, MCP servers or environment) needs a new Claude process, which would stop them; wait for them to finish, or stop them from the background work list, then send the message again")

func (s *claudeSDKSession) scheduledWakePendingLocked(now time.Time) bool {
	return !s.wakeAt.IsZero() && now.Before(s.wakeAt.Add(claudeSDKScheduledWakeMargin))
}

// setScheduledWake records Claude's latest ScheduleWakeup (zero: none or
// stopped). A parked warm query keeps its idle bound until that wake.
func (s *claudeSDKSession) setScheduledWake(at time.Time) {
	s.mu.Lock()
	changed := !s.wakeAt.Equal(at)
	s.wakeAt = at
	if changed && s.parked && s.warm && !s.closing {
		s.armIdleLocked()
	}
	s.mu.Unlock()
	if changed {
		if at.IsZero() {
			s.logf("[SDK_BACKGROUND] scheduled wake cleared session=%s", s.owner)
		} else {
			s.logf("[SDK_BACKGROUND] scheduled wake session=%s in=%s", s.owner, time.Until(at).Round(time.Second))
		}
	}
}

// claudeSDKWakeGrace is how long a kept session whose background work all
// finished waits for Claude's own turn. Claude starts it within a second or
// two of the task notification.
var claudeSDKWakeGrace = 30 * time.Second

// claudeSDKWakeHoldLimit bounds how long a held wake waits for its turn: a
// lost adopt, a crash or a stuck chat must never leave the session paused with
// nobody reading it.
var claudeSDKWakeHoldLimit = 60 * time.Second
var claudeSDKWakeNextID atomic.Uint64

const claudeSDKWarmLimit = 4

var claudeSDKWarmIdle = 15 * time.Minute

type claudeSDKWarmMemory struct {
	BridgeRSSBytes int64 `json:"bridge_rss_bytes"`
	NativePID      int   `json:"native_pid"`
}

// A lifecycle snapshot, never a poller. Only the exact native child PID captured
// by this bridge is sampled; failures are unknown, not zero-byte native memory.
func (s *claudeSDKSession) logWarmMemory(memory *claudeSDKWarmMemory) {
	if s.logger == nil {
		return
	}
	nativeRSS := int64(-1)
	if memory.NativePID > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		raw, err := exec.CommandContext(ctx, "ps", "-o", "rss=", "-p", strconv.Itoa(memory.NativePID)).Output()
		cancel()
		if err == nil {
			if kb, err := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64); err == nil && kb > 0 {
				nativeRSS = kb * 1024
			}
		}
	}
	s.logf("[CLAUDE_WARM] session=%s pid=%d action=memory native_pid=%d bridge_rss_bytes=%d native_rss_bytes=%d", s.owner, s.cmd.Process.Pid, memory.NativePID, memory.BridgeRSSBytes, nativeRSS)
}

// startClaudeSDKSession starts the bridge. cleanup runs once the process has exited.
func startClaudeSDKSession(owner string, launch claudeSDKLaunch, cmd *exec.Cmd, stdin io.WriteCloser, stdout io.Reader, stderr *claudeLockedBuffer, kill context.CancelFunc, cleanup func(), logger interfaces.Logger) (*claudeSDKSession, error) {
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	s := &claudeSDKSession{owner: owner, launch: launch, cmd: cmd, input: &claudeSDKInput{w: stdin}, reader: startClaudeLineReader(stdout), stderr: stderr, exited: make(chan struct{}), kill: kill, logger: logger, process: llmtypes.NewAgentProcess("claude-code", owner)}
	go func() {
		<-s.reader.done
		s.waitErr = cmd.Wait()
		kill()
		_ = stdin.Close()
		cleanup()
		// However the process ended (tasks finished, Stop, an interrupted turn,
		// a crash), the chat's list must not keep showing tasks that died with it.
		s.mu.Lock()
		exitReason := "process_exited"
		if s.closing {
			exitReason = "stopped"
		}
		report := !s.closing && (len(s.tasks) > 0 || (s.parked && !s.warm))
		wasWarm := s.warm
		s.closing = true
		s.tasks = nil
		if s.idle != nil {
			s.idle.Stop()
		}
		s.mu.Unlock()
		s.unregister()
		s.process.Exited(s.logger, exitReason)
		if wasWarm {
			s.logf("[CLAUDE_WARM] session=%s pid=%d action=closed reason=process_exited", s.owner, s.cmd.Process.Pid)
		}
		if report {
			s.logf("[SDK_BACKGROUND] stopped session=%s reason=process_exited", s.owner)
			notifyClaudeSDKBackground(SDKBackgroundUpdate{Owner: s.owner, Closed: "finished"})
		}
		close(s.exited)
	}()
	return s, nil
}

func (s *claudeSDKSession) hasExited() bool {
	select {
	case <-s.exited:
		return true
	default:
		return false
	}
}

func (s *claudeSDKSession) setNativeID(id string) {
	if id = strings.TrimSpace(id); id != "" {
		s.mu.Lock()
		s.nativeID = id
		s.mu.Unlock()
	}
}

func (s *claudeSDKSession) setTasks(tasks []SDKBackgroundTask) {
	s.mu.Lock()
	hadTasks := len(s.tasks) > 0
	s.tasks = tasks
	if s.parked && s.warm && hadTasks != (len(tasks) > 0) {
		s.armIdleLocked()
		if hadTasks && len(tasks) == 0 {
			s.armWakeGraceLocked()
		}
	}
	s.mu.Unlock()
	notifyClaudeSDKBackground(SDKBackgroundUpdate{Owner: s.owner, Tasks: tasks})
}

func (s *claudeSDKSession) taskCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.tasks)
}

func (s *claudeSDKSession) logf(format string, args ...any) {
	if s.logger != nil {
		s.logger.Infof(format, args...)
	}
}

var claudeSDKKept = struct {
	sync.Mutex
	sessions map[string]*claudeSDKSession
}{sessions: map[string]*claudeSDKSession{}}

// park keeps the session after its turn: it starts the idle bound and a reader
// that handles output while no turn owns the session.
func (s *claudeSDKSession) park() {
	s.mu.Lock()
	s.parked = true
	s.lastUsed = time.Now()
	s.drainStop, s.drainDone = make(chan struct{}), make(chan struct{})
	stop, done := s.drainStop, s.drainDone
	idle := s.armIdleLocked()
	tasks := len(s.tasks)
	warm, nativeID := s.warm, s.nativeID
	s.mu.Unlock()
	claudeSDKKept.Lock()
	previous := claudeSDKKept.sessions[s.owner]
	claudeSDKKept.sessions[s.owner] = s
	claudeSDKKept.Unlock()
	if previous != nil && previous != s {
		previous.closeIfParked("replaced", "stopped", "another session kept for this chat")
	}
	if tasks > 0 {
		s.logf("[SDK_BACKGROUND] kept session=%s tasks=%d reason=own_background_tasks_running idle_limit=%s", s.owner, tasks, idle)
	}
	if warm {
		s.logf("[CLAUDE_WARM] session=%s native=%s pid=%d action=kept tasks=%d idle_limit=%s", s.owner, nativeID, s.cmd.Process.Pid, tasks, idle)
	}
	go s.drain(stop, done)
	trimClaudeSDKWarmSessions()
}

// One existing idle timer; active background work retains its previous bound.
func (s *claudeSDKSession) armIdleLocked() time.Duration {
	if s.idle != nil {
		s.idle.Stop()
	}
	idle := persistentInteractiveIdleTimeout()
	if s.warm && len(s.tasks) == 0 && idle > claudeSDKWarmIdle {
		idle = claudeSDKWarmIdle
	}
	// A scheduled wake (at most an hour ahead) outlives the idle bound.
	if now := time.Now(); s.warm && s.scheduledWakePendingLocked(now) {
		if until := s.wakeAt.Add(claudeSDKScheduledWakeMargin).Sub(now); until > idle {
			idle = until
		}
	}
	s.idle = time.AfterFunc(idle, func() { s.closeIfParked("idle", "expired", "idle") })
	return idle
}

// Cap only task-free idle queries. A held/just-finishing wake retains its exact
// receipt until adoption or the existing wake grace; never evict active work.
func trimClaudeSDKWarmSessions() {
	for {
		claudeSDKKept.Lock()
		var oldest *claudeSDKSession
		var oldestUse time.Time
		count := 0
		for _, s := range claudeSDKKept.sessions {
			s.mu.Lock()
			eligible := s.warm && s.parked && !s.closing && len(s.tasks) == 0 && s.wakeFirst == "" && s.wakeGrace == nil && !s.scheduledWakePendingLocked(time.Now())
			used := s.lastUsed
			s.mu.Unlock()
			if eligible {
				count++
				if oldest == nil || used.Before(oldestUse) {
					oldest, oldestUse = s, used
				}
			}
		}
		claudeSDKKept.Unlock()
		if count <= claudeSDKWarmLimit {
			return
		}
		oldest.closeIfParked("evicted", "stopped", "cap")
	}
}

// drain reads the kept session's output while no turn owns it. When Claude
// starts a turn on its own (after a task notification) draining pauses at its
// first line and the host is told, so a chat turn can adopt it (PLAT-746-899);
// a permission request is refused because nobody is there to answer it; an
// empty task list closes the session unless Claude wakes within the grace.
// It gets its channels from the caller: a turn may take the session (and clear
// them) before this goroutine runs.
func (s *claudeSDKSession) drain(stop <-chan struct{}, done chan struct{}) {
	defer close(done)
	for {
		select {
		case <-stop:
			return
		case line, ok := <-s.reader.lines:
			if !ok {
				s.interruptHelpers()
				return // all task bookends have now been consumed
			}
			if s.handleKeptLine(line) {
				return
			}
		}
	}
}

// handleKeptLine reports true when Claude started its own turn; the line is
// then held as wakeFirst and draining stops.
func (s *claudeSDKSession) handleKeptLine(line string) bool {
	line = strings.TrimSpace(line)
	if line == "" || line[0] != '{' {
		return false
	}
	var ev claudeStreamEvent
	if json.Unmarshal([]byte(line), &ev) != nil {
		return false
	}
	if ev.SDKParkedTurn && (ev.Type == "assistant" || ev.Type == "stream_event") && s.holdWake(line) {
		return true
	}
	switch ev.Type {
	case "prompt_suggestion", "sdk_prompt_skipped":
		s.completeNextPrompt(ev)
	case "sdk_helper":
		if ev.Helper != nil {
			s.observeHelper(*ev.Helper)
		}
	case "sdk_background":
		tasks := claudeSDKTasksFromWire(ev.Background)
		s.setTasks(tasks)
		if len(tasks) == 0 {
			s.armWakeGrace()
		}
	case "sdk_scheduled_wake":
		s.setScheduledWake(claudeSDKScheduledWakeTime(ev.ScheduledFor))
	case "sdk_task_output":
		emitClaudeTaskOutput(s.owner, ev.TaskOutput)
	case "sdk_elicitation":
		s.logf("[SDK_BACKGROUND] cancelled form request between turns session=%s", s.owner)
		_ = s.input.write(map[string]any{"type": "permission", "id": ev.RequestID, "response": map[string]any{"action": "cancel"}})
	case "sdk_permission":
		s.logf("[SDK_BACKGROUND] refused tool request between turns session=%s tool=%s", s.owner, ev.ToolName)
		_ = s.input.write(map[string]any{"type": "permission", "id": ev.RequestID, "response": map[string]any{"behavior": "deny", "message": "Nobody is in this chat turn to answer. Ask again in chat."}})
	case "result":
		if ev.SDKParkedTurn {
			s.mu.Lock()
			s.wakeDismissed = false
			s.mu.Unlock()
		}
		output := 0
		if ev.Usage != nil {
			output = ev.Usage.OutputTokens
		}
		s.logf("[SDK_BACKGROUND] dropped a model turn between chat turns session=%s output_tokens=%d is_error=%t", s.owner, output, ev.IsError)
	}
	return false
}

// armWakeGrace closes the session if Claude does not start its own turn
// within claudeSDKWakeGrace of its last task finishing.
func (s *claudeSDKSession) armWakeGrace() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.armWakeGraceLocked()
}

func (s *claudeSDKSession) armWakeGraceLocked() {
	if s.wakeGrace == nil && s.wakeFirst == "" {
		s.wakeGrace = time.AfterFunc(claudeSDKWakeGrace, func() {
			s.mu.Lock()
			s.wakeGrace = nil
			warm := s.warm
			s.mu.Unlock()
			if warm {
				trimClaudeSDKWarmSessions()
			} else {
				s.closeIfParked("finished", "stopped", "tasks_finished")
			}
		})
	}
}

// holdWake keeps the first line of Claude's own turn and tells the host.
func (s *claudeSDKSession) holdWake(line string) bool {
	s.mu.Lock()
	if !s.parked || s.closing || s.wakeDismissed {
		s.mu.Unlock()
		return false
	}
	s.wakeFirst = line
	s.wakeID = claudeSDKWakeNextID.Add(1)
	// A turn Claude starts on its own near its scheduled time is that wake.
	if now := time.Now(); !s.wakeAt.IsZero() && !now.Before(s.wakeAt.Add(-claudeSDKScheduledWakeMargin)) {
		s.wakeAt = time.Time{}
	}
	nativeID, wakeID := s.nativeID, s.wakeID
	if s.wakeGrace != nil {
		s.wakeGrace.Stop()
		s.wakeGrace = nil
	}
	s.wakeHold = time.AfterFunc(claudeSDKWakeHoldLimit, func() { s.dismissWake("wake_not_adopted") })
	s.mu.Unlock()
	s.logf("[SDK_BACKGROUND] wake session=%s: Claude started a turn on its own", s.owner)
	notifyClaudeSDKBackground(SDKBackgroundUpdate{Owner: s.owner, NativeSessionID: nativeID, WakeID: wakeID, Wake: true})
	return true
}

func (s *claudeSDKSession) unregister() {
	claudeSDKKept.Lock()
	if claudeSDKKept.sessions[s.owner] == s {
		delete(claudeSDKKept.sessions, s.owner)
	}
	claudeSDKKept.Unlock()
}

// closeIfParked closes a kept session: Claude's own background tasks are
// stopped, input ends and the process exits (killed if it does not within a
// short grace). A session a turn has taken is left to that turn.
func (s *claudeSDKSession) closeIfParked(closed, verb, reason string) bool {
	s.mu.Lock()
	if !s.parked || s.closing {
		s.mu.Unlock()
		return false
	}
	if (reason == "cap" || reason == "idle_compaction") && (!s.warm || len(s.tasks) > 0 || s.wakeFirst != "" || s.wakeGrace != nil || s.scheduledWakePendingLocked(time.Now())) {
		s.mu.Unlock()
		return false
	}
	s.closing = true
	if s.idle != nil {
		s.idle.Stop()
	}
	if s.wakeGrace != nil {
		s.wakeGrace.Stop()
	}
	// Draining paused at Claude's own turn; read the rest so the process can end.
	paused := s.wakeFirst != ""
	s.wakeFirst = ""
	s.stopWakeHoldLocked()
	tasks := len(s.tasks)
	warm, nativeID := s.warm, s.nativeID
	scheduledWake := s.scheduledWakePendingLocked(time.Now())
	s.tasks = nil
	s.wakeAt = time.Time{}
	s.mu.Unlock()
	s.unregister()
	if scheduledWake {
		s.logf("[SDK_BACKGROUND] scheduled wake dropped session=%s reason=%s", s.owner, reason)
	}
	if tasks > 0 || !warm {
		s.logf("[SDK_BACKGROUND] %s session=%s tasks=%d reason=%s", verb, s.owner, tasks, reason)
		notifyClaudeSDKBackground(SDKBackgroundUpdate{Owner: s.owner, Closed: closed})
	}
	if warm {
		s.logf("[CLAUDE_WARM] session=%s native=%s pid=%d action=%s reason=%s", s.owner, nativeID, s.cmd.Process.Pid, closed, reason)
	}
	s.shutdown()
	if paused {
		go func() {
			for line := range s.reader.lines {
				s.handleKeptLine(line)
			}
			s.interruptHelpers()
		}()
	}
	return true
}

const claudeSDKCloseGrace = 15 * time.Second

func (s *claudeSDKSession) shutdown() {
	_ = s.input.write(map[string]any{"type": "stop_background", "id": randomHex(12)})
	go func() {
		select {
		case <-s.exited:
		case <-time.After(claudeSDKCloseGrace):
			s.logf("[SDK_BACKGROUND] session=%s did not exit after stop; ending the process", s.owner)
			s.kill()
		}
	}()
}

// endReleased ends a session whose turn was released at its answer: input
// ends, remaining output is discarded, and the process is ended if it does not
// exit within a short grace.
func (s *claudeSDKSession) endReleased() {
	go func() {
		for line := range s.reader.lines {
			s.handleKeptLine(line)
		}
		s.interruptHelpers()
	}()
	go func() {
		select {
		case <-s.exited:
		case <-time.After(claudeSDKCloseGrace):
			s.logf("[SDK_BACKGROUND] session=%s did not exit after its turn was released; ending the process", s.owner)
			s.kill()
		}
	}()
}

// takeKeptClaudeSDKSession hands a chat's kept session to its next turn when
// the launch is unchanged and the turn continues the same conversation.
// Otherwise the kept session is closed and the caller starts a new one.
func takeKeptClaudeSDKSession(owner string, launch claudeSDKLaunch, resumeSessionID string) *claudeSDKSession {
	s, _ := takeKeptClaudeSDKSessionWithReason(owner, launch, resumeSessionID)
	return s
}

// claudeSDKRelaunchBlockedReason: the kept session stays for its running
// background tasks and the turn must fail with ErrClaudeBackgroundWorkBlocksRelaunch.
const claudeSDKRelaunchBlockedReason = "background_work_blocks_relaunch"

// claudeSDKOnlyProgramUpdated: the launch differs only in the installed
// Claude program or SDK version.
func claudeSDKOnlyProgramUpdated(reason string) bool {
	parts, found := strings.CutPrefix(reason, "launch_changed:")
	if !found || parts == "" {
		return false
	}
	for _, part := range strings.Split(parts, ",") {
		if part != "binary" && part != "sdk" {
			return false
		}
	}
	return true
}

// claudeSDKScheduledWakeTime converts the bridge's epoch milliseconds.
func claudeSDKScheduledWakeTime(ms int64) time.Time {
	if ms <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}

// takeKeptClaudeSDKSessionWithReason also says why no kept session could be
// used, for the [AGENT_PROCESS] launch line (names only, never values).
func takeKeptClaudeSDKSessionWithReason(owner string, launch claudeSDKLaunch, resumeSessionID string) (*claudeSDKSession, string) {
	claudeSDKKept.Lock()
	s := claudeSDKKept.sessions[owner]
	delete(claudeSDKKept.sessions, owner)
	claudeSDKKept.Unlock()
	if s == nil {
		return nil, "no_kept_process"
	}
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		return nil, "kept_process_closing"
	}
	ok, reason := claudeSDKReuseDecision(s.launch, launch, s.nativeID, resumeSessionID)
	if s.hasExited() {
		ok, reason = false, "process_exited"
	}
	if !ok && len(s.tasks) > 0 && claudeSDKOnlyProgramUpdated(reason) {
		// An installed Claude or SDK update changes nothing this turn may
		// use: keep running the old program until its background work ends;
		// the turn after that starts the new one (#3128).
		ok = true
		s.logf("[CLAUDE_WARM] session=%s pid=%d action=process_restart_deferred reason=%s background_tasks=%d", owner, s.cmd.Process.Pid, reason, len(s.tasks))
	}
	if !ok && strings.HasPrefix(reason, "launch_changed:") && len(s.tasks) > 0 {
		// A relaunch would stop Claude's running background work (a dev
		// server, a build): keep the session for that work and refuse this
		// turn instead (#3128). A conversation change, Stop and chat close
		// still end it. The digest names say what changed, never values.
		tasks := len(s.tasks)
		s.mu.Unlock()
		claudeSDKKept.Lock()
		if claudeSDKKept.sessions[owner] == nil {
			claudeSDKKept.sessions[owner] = s
		}
		claudeSDKKept.Unlock()
		s.logf("[CLAUDE_WARM] session=%s pid=%d action=process_restart_refused reason=%s background_tasks=%d", owner, s.cmd.Process.Pid, reason, tasks)
		return nil, claudeSDKRelaunchBlockedReason
	}
	if !ok {
		s.mu.Unlock()
		s.logf("[CLAUDE_WARM] session=%s pid=%d action=process_restart reason=%s", owner, s.cmd.Process.Pid, reason)
		// Still parked: its reader keeps consuming output until the process exits.
		s.closeIfParked("replaced", "stopped", reason)
		return nil, reason
	}
	tasks := s.unparkLocked()
	s.mu.Unlock()
	s.stopDrain()
	s.logf("[SDK_BACKGROUND] reused session=%s tasks=%d", owner, tasks)
	s.logf("[CLAUDE_WARM] session=%s native=%s pid=%d action=process_reuse reason=launch_unchanged", owner, resumeSessionID, s.cmd.Process.Pid)
	return s, ""
}

// unparkLocked hands the parked session to a turn. A turn that is not the
// adopting one drops a held wake: the bridge keeps Claude's own turn apart from
// the person's prompt, and Claude's conversation keeps what it said.
func (s *claudeSDKSession) unparkLocked() int {
	s.parked = false
	s.wakeFirst = ""
	s.stopWakeHoldLocked()
	if s.idle != nil {
		s.idle.Stop()
	}
	if s.wakeGrace != nil {
		s.wakeGrace.Stop()
		s.wakeGrace = nil
	}
	return len(s.tasks)
}

func (s *claudeSDKSession) stopDrain() {
	s.mu.Lock()
	stop, done := s.drainStop, s.drainDone
	s.drainStop = nil
	s.mu.Unlock()
	if stop != nil {
		close(stop)
		<-done
	}
}

// ErrClaudeWakeGone: the turn Claude started on its own was no longer waiting
// (the session closed, or a person's message took it first).
var ErrClaudeWakeGone = errors.New("claude-code: Claude's own follow-up turn is no longer waiting")

// takeClaudeSDKWake hands a chat's kept session to the turn that adopts the
// turn Claude started on its own, with that turn's first line. The launch is
// not compared: the turn is Claude's, already running in this process.
func takeClaudeSDKWake(owner string) (*claudeSDKSession, string) {
	claudeSDKKept.Lock()
	s := claudeSDKKept.sessions[owner]
	if s != nil {
		s.mu.Lock()
		ready := s.parked && !s.closing && s.wakeFirst != "" && !s.hasExited()
		s.mu.Unlock()
		if !ready {
			s = nil
		} else {
			delete(claudeSDKKept.sessions, owner)
		}
	}
	claudeSDKKept.Unlock()
	if s == nil {
		return nil, ""
	}
	s.mu.Lock()
	first := s.wakeFirst
	tasks := s.unparkLocked()
	s.mu.Unlock()
	s.stopDrain()
	s.logf("[SDK_BACKGROUND] adopted wake session=%s tasks=%d", owner, tasks)
	return s, first
}

// DismissClaudeSDKWake: the chat does not show the turn Claude started on its
// own (the person pressed Stop, another turn holds the chat, or the follow-up
// limit). A session still running Claude's tasks keeps them and reads that turn
// without showing it; a session with none left closes.
func DismissClaudeSDKWake(owner, reason string) {
	claudeSDKKept.Lock()
	s := claudeSDKKept.sessions[strings.TrimSpace(owner)]
	claudeSDKKept.Unlock()
	if s != nil {
		s.dismissWake(reason)
	}
}

func (s *claudeSDKSession) stopWakeHoldLocked() {
	if s.wakeHold != nil {
		s.wakeHold.Stop()
		s.wakeHold = nil
	}
}

func (s *claudeSDKSession) dismissWake(reason string) {
	s.mu.Lock()
	if !s.parked || s.closing || s.wakeFirst == "" {
		s.mu.Unlock()
		return
	}
	if len(s.tasks) == 0 {
		s.mu.Unlock()
		s.closeIfParked("stopped", "stopped", reason)
		return
	}
	s.wakeFirst, s.wakeDismissed = "", true
	s.stopWakeHoldLocked()
	s.drainStop, s.drainDone = make(chan struct{}), make(chan struct{})
	stop, done := s.drainStop, s.drainDone
	tasks := len(s.tasks)
	s.mu.Unlock()
	s.logf("[SDK_BACKGROUND] wake not shown session=%s tasks=%d reason=%s", s.owner, tasks, reason)
	go s.drain(stop, done)
}

// ClaudeSDKWakePending reports whether Claude started a turn on its own in this
// chat's kept session and it is waiting to be shown.
func ClaudeSDKWakePending(owner string) bool {
	claudeSDKKept.Lock()
	s := claudeSDKKept.sessions[strings.TrimSpace(owner)]
	claudeSDKKept.Unlock()
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.parked && !s.closing && s.wakeFirst != ""
}

// closeKeptClaudeSDKSession closes a chat's kept session, stopping its tasks.
func closeKeptClaudeSDKSession(owner, reason string) bool {
	claudeSDKKept.Lock()
	s := claudeSDKKept.sessions[strings.TrimSpace(owner)]
	claudeSDKKept.Unlock()
	if s == nil {
		return false
	}
	return s.closeIfParked("stopped", "stopped", reason)
}

func closeAllKeptClaudeSDKSessions() {
	claudeSDKKept.Lock()
	sessions := make([]*claudeSDKSession, 0, len(claudeSDKKept.sessions))
	for _, s := range claudeSDKKept.sessions {
		sessions = append(sessions, s)
	}
	claudeSDKKept.Unlock()
	for _, s := range sessions {
		s.closeIfParked("stopped", "stopped", "shutdown")
	}
}

func claudeSDKSessionForOwner(owner string) *claudeSDKSession {
	owner = strings.TrimSpace(owner)
	claudeSDKKept.Lock()
	kept := claudeSDKKept.sessions[owner]
	claudeSDKKept.Unlock()
	if kept != nil {
		return kept
	}
	if process, ok := claudeSDKProcesses.Get(owner); ok {
		return process.session
	}
	return nil
}

// ClaudeSDKBackgroundTasks lists the background tasks Claude started itself in
// this chat that are still running, during a turn or between turns.
func ClaudeSDKBackgroundTasks(owner string) []SDKBackgroundTask {
	s := claudeSDKSessionForOwner(owner)
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		return nil
	}
	return append([]SDKBackgroundTask(nil), s.tasks...)
}

// StopClaudeSDKBackgroundTask stops one background task Claude started itself
// in this chat. When it was the last one of a kept session, the empty list that
// follows closes the session. False when the chat has no such task.
func StopClaudeSDKBackgroundTask(owner, taskID string) bool {
	s := claudeSDKSessionForOwner(owner)
	if s == nil {
		return false
	}
	found := false
	for _, task := range ClaudeSDKBackgroundTasks(owner) {
		found = found || task.TaskID == taskID
	}
	if !found {
		return false
	}
	s.logf("[SDK_BACKGROUND] stop requested session=%s task=%s", s.owner, taskID)
	return s.input.write(map[string]any{"type": "stop_task", "task_id": taskID}) == nil
}

// QueueClaudeSDKWake confirms that the host accepted this exact wake into its
// existing turn lane. A queued turn is not abandoned just because another turn
// lasts longer than the unclaimed-wake timeout. Stop/process close still apply.
func QueueClaudeSDKWake(owner, native string, wakeID uint64) bool {
	claudeSDKKept.Lock()
	s := claudeSDKKept.sessions[strings.TrimSpace(owner)]
	claudeSDKKept.Unlock()
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.parked || s.closing || s.nativeID != native || s.wakeFirst == "" || s.wakeID != wakeID {
		return false
	}
	s.stopWakeHoldLocked()
	return true
}

// ClaudeSDKWakeIdentity is only a correlation fence, not a permission grant.
func ClaudeSDKWakeIdentity(owner string) (string, uint64) {
	claudeSDKKept.Lock()
	s := claudeSDKKept.sessions[strings.TrimSpace(owner)]
	claudeSDKKept.Unlock()
	if s == nil {
		return "", 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.parked || s.closing || s.wakeFirst == "" {
		return "", 0
	}
	return s.nativeID, s.wakeID
}

// Native suggestions arrive after result, while the kept session drains. Match
// the exact host turn before delivering to its captured host callback.
func (s *claudeSDKSession) completeNextPrompt(event claudeStreamEvent) {
	s.mu.Lock()
	scope := s.nextPrompt
	native := s.nativeID
	s.mu.Unlock()
	if scope.Complete == nil || scope.TurnID != event.SDKPromptTurn {
		return
	}
	scope.Finish(llmtypes.NextPromptResult{Text: event.Suggestion, Reason: event.PromptSkipReason, NativeThread: native})
}
