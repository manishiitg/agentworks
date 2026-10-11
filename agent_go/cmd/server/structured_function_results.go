package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/schedulerstate"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

type structuredFunctionCallerSessionKey struct{}

const structuredFunctionMaxRunning = 3
const structuredFunctionJSONCap = 16 << 20
const structuredFunctionInlineCap = 256 << 10
const structuredFunctionReadCap = 2 << 20

type structuredFunctionFile struct {
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	MIMEType string `json:"mime_type"`
}
type structuredFunctionMessage struct {
	Role    string    `json:"role"`
	Content string    `json:"content"`
	At      time.Time `json:"at"`
}

func isBuiltinConversationalFunction(target triggerTarget, fn crewFunction) bool {
	return isWorkflowAsk(target, fn) || isPulseBuilderAsk(target, fn) || isGoalLeadAsk(target, fn) || (fn.Name == crewFunctionAskName && fn.CreatedBy == defaultAskCrewFunction().CreatedBy)
}

// Admission and insertion share crewFunctionCalls.Lock: two callers cannot both
// claim the last slot. Timed-out but still live executions retain their slot.
func admitStructuredFunctionLocked(candidate *crewFunctionCall) error {
	if candidate.TargetKind != triggerCallerCrew || !candidate.IsolatedExecution {
		return nil
	}
	running := 0
	for _, call := range crewFunctionCalls.m {
		call.mu.Lock()
		active := call.IsolatedExecution && call.TargetKind == candidate.TargetKind && canonicalCrewWorkspaceRoot(call.TargetPath) == canonicalCrewWorkspaceRoot(candidate.TargetPath) && call.admissionHeld
		call.mu.Unlock()
		if active {
			running++
		}
	}
	if running >= structuredFunctionMaxRunning {
		return fmt.Errorf("busy: Crew %q already has %d function executions running (limit %d); no call was queued; decide whether to retry later", candidate.TargetLabel, running, structuredFunctionMaxRunning)
	}
	return nil
}

const structuredFunctionOutputKeep = 7 * 24 * time.Hour

var structuredFunctionLastSweep sync.Map

// sweepStructuredFunctionOutputs removes a Crew's per-call output folders a
// week after their last change, at most once an hour per Crew. Folders of
// calls still held in memory as running are left alone.
func sweepStructuredFunctionOutputs(targetPath string) {
	key := canonicalCrewWorkspaceRoot(targetPath)
	if last, ok := structuredFunctionLastSweep.Load(key); ok && time.Since(last.(time.Time)) < time.Hour {
		return
	}
	structuredFunctionLastSweep.Store(key, time.Now())
	dir := filepath.Join(getWorkspaceDocsAbsPath(), filepath.FromSlash(strings.TrimSuffix(targetPath, "/")), ".calls")
	if info, err := os.Lstat(dir); err != nil || !info.IsDir() {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "fn-") {
			continue // never follow or remove links or stray files
		}
		info, err := e.Info()
		if err != nil || time.Since(info.ModTime()) < structuredFunctionOutputKeep {
			continue
		}
		if call := lookupCrewFunctionCall(e.Name()); call != nil {
			call.mu.Lock()
			held := call.admissionHeld
			call.mu.Unlock()
			if held {
				continue
			}
		}
		_ = os.RemoveAll(filepath.Join(dir, e.Name()))
	}
}

func (c *crewFunctionCall) outputRelativeFolder() string { return ".calls/" + c.ID }
func (c *crewFunctionCall) outputFolder() string {
	return strings.TrimSuffix(c.TargetPath, "/") + "/" + c.outputRelativeFolder()
}
func (c *crewFunctionCall) resultRelativePath() string {
	return c.outputRelativeFolder() + "/result.json"
}

func structuredFunctionOutputInstructions(call *crewFunctionCall) string {
	if len(call.ResultSchema) == 0 {
		return "No structured result is required. Your final message is the answer. Put any requested file outputs in the call's output folder."
	}
	schema, _ := json.MarshalIndent(call.ResultSchema, "", "  ")
	return fmt.Sprintf("Required output: write JSON matching this schema to $FUNCTION_RESULT_FILE (%s). Write the complete file before finishing; the platform validates it after your turn ends. Your final message explains the outcome, separately from the JSON file.\n%s", call.resultRelativePath(), schema)
}

// Files are read through a root opened on the shared mount, rejecting every
// symbolic-link component. A call permission must be checked by the entrypoint
// before calling these helpers; a sibling .calls folder is never admitted.
func openStructuredFunctionFolder(call *crewFunctionCall) (*os.Root, error) {
	if !strings.HasPrefix(call.ID, "fn-") || strings.ContainsAny(call.ID, "/\\") || strings.Contains(call.ID, "..") {
		return nil, fmt.Errorf("invalid call identifier")
	}
	p, err := wf.CleanRelative(call.outputFolder())
	if err != nil {
		return nil, err
	}
	base, err := os.OpenRoot(getWorkspaceDocsAbsPath())
	if err != nil {
		return nil, err
	}
	defer base.Close()
	if err := externalNoSymlinks(base, p); err != nil {
		return nil, err
	}
	return base.OpenRoot(p)
}

func listStructuredFunctionFiles(call *crewFunctionCall) ([]structuredFunctionFile, error) {
	if call.TargetKind == triggerCallerWorkflow {
		call.mu.Lock()
		defer call.mu.Unlock()
		return append([]structuredFunctionFile(nil), call.Files...), nil
	}
	files := []structuredFunctionFile{}
	root, err := openStructuredFunctionFolder(call)
	if errors.Is(err, fs.ErrNotExist) {
		return files, nil
	}
	if err != nil {
		return nil, err
	}
	defer root.Close()
	err = fs.WalkDir(root.FS(), ".", func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if p == "." {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") || d.Type()&os.ModeSymlink != 0 {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		if len(files) >= 100 {
			return fs.SkipAll
		}
		files = append(files, structuredFunctionFile{Name: p, Size: info.Size(), MIMEType: structuredFunctionMIME(p)})
		return nil
	})
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	return files, err
}
func structuredFunctionMIME(name string) string {
	t := mime.TypeByExtension(path.Ext(name))
	if t == "" {
		t = "application/octet-stream"
	}
	return t
}

func readCrewFunctionOutput(ctx context.Context, call *crewFunctionCall, name string, offset, limit int) (map[string]interface{}, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p, err := wf.CleanRelative(name)
	if err != nil || p == "." {
		return nil, fmt.Errorf("invalid output file name")
	}
	for _, part := range strings.Split(p, "/") {
		if strings.HasPrefix(part, ".") {
			return nil, fmt.Errorf("private output file")
		}
	}
	if offset < 0 {
		return nil, fmt.Errorf("offset must be nonnegative")
	}
	if limit <= 0 {
		limit = structuredFunctionInlineCap
	}
	if limit > structuredFunctionReadCap {
		limit = structuredFunctionReadCap
	}
	root, err := openStructuredFunctionOutputRoot(call, p)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	if err := externalNoSymlinks(root, p); err != nil {
		return nil, err
	}
	f, err := root.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("output is not a regular file")
	}
	if int64(offset) > st.Size() {
		return nil, fmt.Errorf("offset exceeds file size")
	}
	if _, err = f.Seek(int64(offset), io.SeekStart); err != nil {
		return nil, err
	}
	raw, err := io.ReadAll(io.LimitReader(f, int64(limit)))
	if err != nil {
		return nil, err
	}
	if int64(offset)+int64(len(raw)) < st.Size() {
		// A page may end inside a multi-byte character; leave that
		// character for the next page so text stays text.
		for cut := 1; cut < utf8.UTFMax && cut <= len(raw); cut++ {
			if r := raw[len(raw)-cut]; utf8.RuneStart(r) {
				if !utf8.FullRune(raw[len(raw)-cut:]) {
					raw = raw[:len(raw)-cut]
				}
				break
			}
		}
	}
	next := int64(offset) + int64(len(raw))
	out := map[string]interface{}{"file": p, "offset": offset, "next_offset": next, "total_size": st.Size(), "has_more": next < st.Size(), "mime_type": structuredFunctionMIME(p)}
	if utf8.Valid(raw) && !strings.ContainsRune(string(raw), 0) && !strings.HasPrefix(structuredFunctionMIME(p), "image/") && structuredFunctionMIME(p) != "application/pdf" {
		out["encoding"] = "utf-8"
		out["content"] = string(raw)
	} else {
		out["encoding"] = "base64"
		out["content_base64"] = base64.StdEncoding.EncodeToString(raw)
	}
	return out, nil
}

func readStructuredFunctionFileResult(call *crewFunctionCall) (interface{}, []string) {
	root, err := openStructuredFunctionFolder(call)
	if err != nil {
		return nil, []string{"result.json is missing or unavailable"}
	}
	defer root.Close()
	if err := externalNoSymlinks(root, "result.json"); err != nil {
		return nil, []string{err.Error()}
	}
	f, err := root.Open("result.json")
	if err != nil {
		return nil, []string{"result.json is missing"}
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		return nil, []string{"result.json must be a regular file"}
	}
	raw, err := io.ReadAll(io.LimitReader(f, structuredFunctionJSONCap+1))
	if err != nil {
		return nil, []string{"result.json could not be read"}
	}
	if len(raw) > structuredFunctionJSONCap {
		return nil, []string{"result.json exceeds the 16 MiB validation limit"}
	}
	var result interface{}
	if json.Unmarshal(raw, &result) != nil {
		return nil, []string{"result.json is not valid JSON"}
	}
	return result, validateCrewFunctionValue(call.ResultSchema, result)
}

// A workflow already has its terminal plan output; it never gets a fabricated
// corrective model turn. Crew file fallback is allowed after one correction.
func readStructuredFunctionResult(ctx context.Context, call *crewFunctionCall, answer string) (interface{}, []string) {
	if err := ctx.Err(); err != nil {
		return nil, []string{err.Error()}
	}
	if len(call.ResultSchema) == 0 {
		if call.TargetKind == triggerCallerWorkflow {
			var decoded interface{}
			if json.Unmarshal([]byte(answer), &decoded) == nil {
				return decoded, nil
			}
		}
		return answer, nil
	}
	result, problems := readStructuredFunctionFileResult(call)
	if len(problems) == 0 {
		return structuredFunctionInlineResult(call, result), nil
	}
	call.mu.Lock()
	fallback := call.Retried || call.TargetKind == triggerCallerWorkflow || !call.IsolatedExecution
	call.mu.Unlock()
	if fallback {
		if decoded, ok := structuredFunctionFinalJSON(answer); ok {
			if issues := validateCrewFunctionValue(call.ResultSchema, decoded); len(issues) == 0 {
				raw, encodeErr := json.Marshal(decoded)
				var saveErr error
				if encodeErr == nil {
					root, err := openStructuredFunctionFolder(call)
					if err == nil {
						if saveErr = externalNoSymlinks(root, "result.json"); saveErr == nil {
							saveErr = root.WriteFile("result.json", raw, 0600)
						}
						_ = root.Close()
					} else {
						saveErr = err
					}
				} else {
					saveErr = encodeErr
				}
				if len(raw) > structuredFunctionInlineCap && saveErr != nil {
					return nil, []string{"validated final JSON could not be saved for paged reading"}
				}

				return structuredFunctionInlineResult(call, decoded), nil
			} else {
				problems = append(problems, issues...)
			}
		}
	}
	return nil, problems
}
func structuredFunctionInlineResult(call *crewFunctionCall, result interface{}) interface{} {
	encoded, _ := json.Marshal(result)
	if len(encoded) <= structuredFunctionInlineCap {
		return result
	}
	return map[string]interface{}{"file": "result.json", "bytes": len(encoded), "read_with": "get_function_call(call_id, file, offset, limit)"}
}
func structuredFunctionFinalJSON(answer string) (interface{}, bool) {
	text := strings.TrimSpace(answer)
	if strings.HasPrefix(text, "```") {
		if i := strings.IndexByte(text, '\n'); i >= 0 {
			text = text[i+1:]
		}
		if i := strings.LastIndex(text, "```"); i >= 0 {
			text = strings.TrimSpace(text[:i])
		}
	}
	var result interface{}
	if json.Unmarshal([]byte(text), &result) == nil {
		return result, true
	}
	// Surrounding explanatory words may be stripped; only a complete object or
	// array is accepted, never a guessed or repaired JSON value.
	for _, pair := range [][2]byte{{'{', '}'}, {'[', ']'}} {
		start, end := strings.IndexByte(text, pair[0]), strings.LastIndexByte(text, pair[1])
		if start >= 0 && end > start && json.Unmarshal([]byte(text[start:end+1]), &result) == nil {
			return result, true
		}
	}
	return nil, false
}

func (c *crewFunctionCall) appendExecutionMessage(role, text string) {
	if len(text) > 64<<10 {
		text = text[:64<<10] + "\n[message truncated]"
	}
	c.mu.Lock()
	c.Messages = append(c.Messages, structuredFunctionMessage{Role: role, Content: text, At: time.Now().UTC()})
	if len(c.Messages) > 200 {
		c.Messages = c.Messages[len(c.Messages)-200:]
		c.MessagesBase++
	}
	c.mu.Unlock()
	c.persist()
}

func (api *StreamingAPI) addCrewFunctionReadDetails(ctx context.Context, out map[string]interface{}, call *crewFunctionCall, after, limit int, eventAfter ...int) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	call.mu.Lock()
	messages := append([]structuredFunctionMessage(nil), call.Messages...)
	base := call.MessagesBase
	call.mu.Unlock()
	start := after + 1 - base
	if start < 0 {
		start = 0
	}
	if start > len(messages) {
		start = len(messages)
	}
	end := min(start+limit, len(messages))
	out["messages"] = messages[start:end]
	out["next_after"] = base + end - 1
	out["has_more_messages"] = end < len(messages)
	if after < base-1 {
		out["messages_cursor_reset"] = true
	}
	if api != nil && api.eventStore != nil {
		cursor := -1
		if len(eventAfter) > 0 {
			cursor = eventAfter[0]
		}
		call.mu.Lock()
		sid := call.SessionID
		call.mu.Unlock()
		if sid != "" {
			_, _ = api.eventStore.GetLatestEventIndex(sid) // bounded durable journal hydration
			page := api.eventStore.GetForwardEventPageBudget(sid, cursor, limit, 64<<10)
			events := []map[string]interface{}{}
			for _, event := range page.Events {
				kind := strings.ToLower(event.Type)
				if event.Data == nil || !(strings.Contains(kind, "assistant") || strings.Contains(kind, "llm_generation_end") || strings.Contains(kind, "unified_completion") || strings.Contains(kind, "conversation_user")) {
					continue
				}
				fields := map[string]interface{}{}
				raw, _ := json.Marshal(event.Data.Data)
				_ = json.Unmarshal(raw, &fields)
				text := crewFunctionEventText(fields)
				if text == "" {
					continue
				}
				if len(text) > 32<<10 {
					text = text[:32<<10] + "\n[message truncated]"
				}
				events = append(events, map[string]interface{}{"id": event.ID, "type": event.Type, "at": event.Timestamp, "text": text})
			}
			out["events"] = events
			out["next_after_event"] = page.LastProcessedIndex
			out["has_more_events"] = page.HasMore
			out["events_cursor_reset"] = page.CursorReset
		}
	}
	if files, err := listStructuredFunctionFiles(call); err == nil {
		out["files"] = files
	}
}

func structuredFunctionShellEnvironment(call *crewFunctionCall) map[string]string {
	return map[string]string{"FUNCTION_CALL_ID": call.ID, "FUNCTION_OUTPUT_DIR": filepath.Join(getWorkspaceDocsAbsPath(), filepath.FromSlash(call.outputFolder())), "FUNCTION_RESULT_FILE": filepath.Join(getWorkspaceDocsAbsPath(), filepath.FromSlash(call.outputFolder()), "result.json")}
}

func persistStructuredFunctionAdmission(ctx context.Context, call *crewFunctionCall) error {
	call.mu.Lock()
	raw, err := json.Marshal(call)
	record := call.recordPath()
	call.mu.Unlock()
	if err != nil {
		return err
	}
	if err := writeFileToWorkspace(ctx, record, string(raw)+"\n"); err != nil {
		return err
	}
	index, err := json.Marshal(crewFunctionCallIndex{TargetPath: call.TargetPath, RecordPath: record, CallerPath: call.CallerPath, UserID: call.UserID})
	if err != nil {
		return err
	}
	return writeFileToWorkspace(ctx, crewFunctionCallIndexPath(call.ID), string(index)+"\n")
}

func releaseStructuredFunctionAdmission(call *crewFunctionCall) {
	if call == nil {
		return
	}
	call.mu.Lock()
	call.admissionHeld = false
	call.mu.Unlock()
}

func captureStructuredFunctionRunState(call *crewFunctionCall, state triggerTargetRunState) {
	switch result := state.Raw.(type) {
	case productWebhookRunStatus:
		call.mu.Lock()
		// Running records can omit the session until their terminal save. Keep
		// the exact receiving session established by the isolated worker.
		if call.SessionID == "" && result.SessionID != "" {
			call.SessionID = result.SessionID
		}
		call.Usage = result.Usage
		call.mu.Unlock()
	case webhookRunResult:
		files := []structuredFunctionFile{}
		for _, step := range result.Steps {
			for _, artifact := range step.Artifacts {
				if webhookOutputPath(artifact.Path) {
					files = append(files, structuredFunctionFile{Name: artifact.Path, Size: artifact.Size, MIMEType: structuredFunctionMIME(artifact.Path)})
				}
			}
		}
		call.mu.Lock()
		call.WorkflowRunFolder = result.RunFolder
		call.Files = files
		call.mu.Unlock()
	}
}

func openStructuredFunctionOutputRoot(call *crewFunctionCall, name string) (*os.Root, error) {
	if call.TargetKind != triggerCallerWorkflow {
		return openStructuredFunctionFolder(call)
	}
	if name == "result.json" {
		call.mu.Lock()
		reference, _ := call.Result.(map[string]interface{})
		allowed := reference["file"] == "result.json"
		call.mu.Unlock()
		if allowed {
			return openStructuredFunctionFolder(call)
		}
	}
	call.mu.Lock()
	folder := call.WorkflowRunFolder
	runID := call.RunID
	allowed := false
	for _, file := range call.Files {
		if file.Name == name {
			allowed = true
			break
		}
	}
	call.mu.Unlock()
	if !allowed || !webhookOutputPath(name) {
		return nil, fmt.Errorf("file is not an output of this function execution")
	}
	base, err := os.OpenRoot(getWorkspaceDocsAbsPath())
	if err != nil {
		return nil, err
	}
	defer base.Close()
	if err := externalNoSymlinks(base, path.Join(call.TargetPath, "runs", folder)); err != nil {
		return nil, err
	}
	return openWebhookRunRoot(call.TargetPath, schedulerstate.Run{RunID: runID, RunFolder: folder})
}
