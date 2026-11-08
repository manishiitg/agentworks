package step_based_workflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/redaction"
	"github.com/manishiitg/coding-agent-loop/workspace/runlog"
	"github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

type pythonSourceCallKey struct{}
type pythonScriptRuntimeKey struct{}

type pythonHelperReply struct {
	OK             bool        `json:"ok"`
	Value          interface{} `json:"value,omitempty"`
	Text           string      `json:"text"`
	AnswerFile     string      `json:"answer_file,omitempty"`
	ConversationID string      `json:"conversation_id"`
	Error          string      `json:"error,omitempty"`
	LogFile        string      `json:"log_file"`
	LogComplete    bool        `json:"log_complete"`
	LogError       string      `json:"log_error,omitempty"`
	LogKind        string      `json:"log_kind,omitempty"`
}

type pythonHelperCall struct {
	ExecutionID   string            `json:"execution_id"`
	SourceHash    string            `json:"source_hash"`
	ArgumentsHash string            `json:"arguments_hash"`
	Complete      bool              `json:"complete"`
	ScriptStack   []string          `json:"script_stack,omitempty"`
	Reply         pythonHelperReply `json:"reply"`
}

// Helper receipts and continuation configuration are server-owned evidence.
// Models and authored Python may read them but must never forge them.
func (hcpo *StepBasedWorkflowOrchestrator) pythonHelperStateRoot() string {
	if hcpo.selectedRunFolder == "" {
		return ""
	}
	return filepath.ToSlash(filepath.Join(hcpo.GetWorkspacePath(), "runs", hcpo.selectedRunFolder, "helpers"))
}

func (hcpo *StepBasedWorkflowOrchestrator) writePythonHelperState(ctx context.Context, file, content string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	state := hcpo.pythonHelperStateRoot()
	full := workflowAPIPath(hcpo.GetWorkspacePath(), file)
	if !filepath.IsAbs(full) && !strings.HasPrefix(full, hcpo.GetWorkspacePath()+"/") {
		full = filepath.Join(hcpo.GetWorkspacePath(), full)
	}
	full = filepath.ToSlash(filepath.Clean(full))
	if state == "" || !strings.HasPrefix(full, state+"/") {
		return fmt.Errorf("helper state must stay in this run")
	}
	root, relative, err := workflowfiles.OpenWorkflowDirectory(GetPromptDocsRoot(), hcpo.GetWorkspacePath(), strings.TrimPrefix(state, hcpo.GetWorkspacePath()+"/"))
	if err != nil {
		return err
	}
	defer root.Close()
	helper, err := workflowfiles.OpenDirectory(root, relative, true)
	if err != nil {
		return err
	}
	defer helper.Close()
	return workflowfiles.WriteServerFile(helper, strings.TrimPrefix(full, state+"/"), []byte(content))
}

// One script launch owns one adapter. Call occurrences, including repeated
// explicit labels in a loop, are distinct. The saved receipt is only a result
// cache across an explicit continuation, not a Python execution journal.
func (hcpo *StepBasedWorkflowOrchestrator) pythonAutomationHandler(parent context.Context, step PlanStepInterface, codeDir, outputDir string) func(context.Context, map[string]interface{}) (string, error) {
	executionID := uuid.NewString()
	scope := sha256.Sum256([]byte(outputDir))
	callRoot := filepath.ToSlash(filepath.Join("runs", hcpo.selectedRunFolder, "helpers", hex.EncodeToString(scope[:12])))
	view := workflowfiles.WorkflowFS{Docs: GetPromptDocsRoot(), Root: hcpo.GetWorkspacePath()}
	var fingerprintOnce sync.Once
	var sourceHash string
	var fingerprintError error
	var locks sync.Map
	var activities sync.Map // Only this script invocation may finish its started activities.
	resume := hcpo.executionOptions != nil && hcpo.executionOptions.AllowRunContinuation && hcpo.executionOptions.ResumeFromStep > 0

	return func(request context.Context, args map[string]interface{}) (string, error) {
		// Keep the original execution identity/inspection context, while a caller
		// disconnect or parent cancellation still cancels this helper invocation.
		ctx, cancel := context.WithCancel(parent)
		ctx = context.WithValue(ctx, pythonScriptRuntimeKey{}, pythonScriptRuntime{SourceDir: codeDir, DependenciesDir: hcpo.scriptDependencies(step)})
		stop := context.AfterFunc(request, cancel)
		defer stop()
		defer cancel()
		encode := func(reply pythonHelperReply) (string, error) {
			if reply.Error != "" && reply.LogFile == "" && reply.LogError == "" {
				if log, err := hcpo.openPythonHelperLog("python-" + uuid.NewString()); err != nil {
					reply.LogError = "Helper log unavailable: " + err.Error()
				} else {
					log.Note("helper not started: " + reply.Error)
					log.Close()
					reply.LogFile, reply.LogError, reply.LogComplete = log.Path(), log.Error(), log.Error() == ""
				}
			}
			data, err := json.Marshal(reply)
			return string(data), err
		}
		action, _ := args["action"].(string)
		label, _ := args["key"].(string)
		order, _ := args["order"].(float64)
		arguments, _ := args["arguments"].(map[string]interface{})
		redo, _ := args["redo"].(bool)
		if label == "" || order < 1 || order != float64(int64(order)) || arguments == nil || (action != "check" && action != "notify" && action != "run_script" && action != "agent" && action != "step" && action != "call_function" && action != "function_status" && action != "send_email" && action != "send_slack" && action != "google" && action != "github" && action != "slack_read") {
			return encode(pythonHelperReply{Error: "Invalid Python helper request; use the installed agentworks package."})
		}
		slot := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d", label, int64(order))))
		stateRelative := filepath.ToSlash(filepath.Join(callRoot, hex.EncodeToString(slot[:])+".json"))
		mutex, _ := locks.LoadOrStore(stateRelative, &sync.Mutex{})
		mutex.(*sync.Mutex).Lock()
		defer mutex.(*sync.Mutex).Unlock()
		fingerprintOnce.Do(func() {
			relative := strings.TrimPrefix(workflowAPIPath(hcpo.GetWorkspacePath(), codeDir), hcpo.GetWorkspacePath()+"/")
			sourceHash, fingerprintError = pythonBundleFingerprint(view, relative)
		})
		if fingerprintError != nil {
			return encode(pythonHelperReply{Error: "Cannot read this script's executed source: " + fingerprintError.Error()})
		}
		callSourceHash := sourceHash
		stack := pythonScriptStack(parent, step)
		var child *CrewScriptConfiguration
		if action == "run_script" {
			var err error
			child, stack, err = hcpo.preparePythonScriptCall(ctx, step, arguments)
			if err != nil {
				return encode(pythonHelperReply{Error: err.Error()})
			}
			fileHashes := map[string]string{}
			for name, data := range child.Files {
				fileHashes[name] = fmt.Sprintf("%x", sha256.Sum256(data))
			}
			bundle, err := json.Marshal(struct {
				Definition CrewScriptDefinition
				Files      map[string]string
			}{child.Definition, fileHashes})
			if err != nil {
				return encode(pythonHelperReply{Error: err.Error()})
			}
			hash := sha256.New()
			_, _ = io.WriteString(hash, sourceHash+"\x00")
			_, _ = hash.Write(bundle)
			callSourceHash = hex.EncodeToString(hash.Sum(nil))
		}
		requestJSON, err := json.Marshal(map[string]interface{}{"action": action, "arguments": arguments})
		if err != nil {
			return "", err
		}
		requestHash := sha256.Sum256(requestJSON)
		argumentsHash := hex.EncodeToString(requestHash[:])
		if data, err := fs.ReadFile(view, stateRelative); action == "function_status" {
			// Status is a fresh authorized read, including after a resumed script.
		} else if err == nil {
			var previous pythonHelperCall
			if err := json.Unmarshal(data, &previous); err != nil {
				return encode(pythonHelperReply{Error: "This helper's saved receipt cannot be read; inspect the run before trying again."})
			}
			if previous.Complete && previous.ExecutionID == executionID {
				// The same HTTP request may be delivered twice. Python assigns a
				// new order to each actual call, so this never skips a loop call.
				if previous.ArgumentsHash != argumentsHash {
					return encode(pythonHelperReply{Error: "This helper request ID was already used with different inputs."})
				}
				return encode(previous.Reply)
			}
			if (action == "run_script" || action == "agent" || action == "call_function" || action == "send_email" || action == "send_slack" || action == "google" || action == "github") && !previous.Complete && !redo {
				reply := previous.Reply
				reply.OK = false
				reply.Error = "The previous script attempt has no completed receipt. Inspect its run before explicitly trying redo=True; it may already have done work."
				return encode(reply)
			}
			if resume {
				if previous.SourceHash != callSourceHash || previous.ArgumentsHash != argumentsHash {
					return encode(pythonHelperReply{Error: "This resumed call has changed code or inputs. Start a new run."})
				}
				if previous.Complete && !redo {
					return encode(previous.Reply)
				}
			}
		} else if !errors.Is(err, fs.ErrNotExist) {
			return encode(pythonHelperReply{Error: "Cannot read this helper's saved receipt: " + err.Error()})
		}
		id := "python-" + uuid.NewString()
		log, err := hcpo.openPythonHelperLog(id)
		if err != nil {
			return encode(pythonHelperReply{Error: "Cannot start this helper's run log.", LogError: err.Error()})
		}
		defer log.Close()
		log.Note(action + " starting")
		_, _ = log.Stream("request").Write(append(requestJSON, '\n'))
		call := pythonHelperCall{ExecutionID: executionID, SourceHash: callSourceHash, ArgumentsHash: argumentsHash, ScriptStack: stack,
			Reply: pythonHelperReply{LogFile: log.Path()}}
		persist := func() error {
			data, err := json.Marshal(call)
			if err != nil {
				return err
			}
			return hcpo.writePythonHelperState(ctx, filepath.ToSlash(filepath.Join(hcpo.GetWorkspacePath(), stateRelative)), string(data))
		}
		if err := persist(); err != nil {
			log.Note("not started: " + err.Error())
			log.Close()
			call.Reply.Error = "Cannot save this helper's receipt: " + err.Error()
			call.Reply.LogComplete, call.Reply.LogError = log.Error() == "", log.Error()
			return encode(call.Reply)
		}
		switch action {
		case "google", "github", "slack_read":
			call.Reply = hcpo.runPythonApp(ctx, action, arguments)
		case "send_email", "send_slack":
			name := "send_email"
			if action == "send_slack" {
				name = "send_slack_message"
				stable := sha256.Sum256([]byte(hcpo.GetWorkspacePath() + "/" + hcpo.selectedRunFolder + "/" + stateRelative))
				arguments["idempotency_key"] = hex.EncodeToString(stable[:])
			}
			execute, ok := hcpo.WorkspaceToolExecutors[name].(func(context.Context, map[string]interface{}) (string, error))
			if !ok || execute == nil {
				call.Reply = pythonHelperReply{Error: "This send tool is unavailable; check the automation's connected account in Crew chat."}
			} else if result, err := execute(ctx, arguments); err != nil {
				call.Reply = pythonHelperReply{Error: err.Error()}
			} else {
				call.Reply = pythonHelperReply{OK: true, Text: result}
			}
		case "call_function", "function_status":
			call.Reply = hcpo.runPythonFunction(ctx, step, action, arguments, id)
		case "agent":
			if conversation, _ := arguments["conversation_id"].(string); conversation != "" {
				mutex, _ := locks.LoadOrStore("conversation:"+conversation, &sync.Mutex{})
				mutex.(*sync.Mutex).Lock()
				defer mutex.(*sync.Mutex).Unlock()
			}
			if source, _ := arguments["source_call"].(string); source != "" {
				ctx = context.WithValue(ctx, pythonSourceCallKey{}, pythonHelperPreview(source))
			}
			call.Reply = hcpo.runPythonAgent(ctx, step, arguments, id)
		case "run_script":
			childCtx := context.WithValue(ctx, pythonScriptStackKey{}, stack)
			call.Reply = hcpo.runPythonSavedScript(childCtx, step, child, arguments, id)
		case "step":
			phase, _ := arguments["phase"].(string)
			if phase == "start" {
				title, _ := arguments["title"].(string)
				source, _ := arguments["source_call"].(string)
				if strings.TrimSpace(title) == "" || source == "" {
					call.Reply.Error = "A step needs its label and source call."
					break
				}
				title = pythonHelperPreview(redaction.Text(title))
				metadata := map[string]string{"source_call": pythonHelperPreview(source), "inspection_kind": "python_step", "run_folder": hcpo.selectedRunFolder, "log_file": log.Path()}
				started := time.Now()
				evidence := PythonStepEvidence{ID: id, Name: title, SourceCall: source, StartedAt: started.UTC().Format(time.RFC3339Nano), Status: "running", LogNote: "This step has no separate program log; see the run script log."}
				if err := hcpo.savePythonStepEvidence(ctx, evidence); err != nil {
					call.Reply.Error = "Cannot save step start: " + err.Error()
					break
				}
				notifier := hcpo.workshopExecutionNotifier
				var once sync.Once
				finish := func(detail string, failure error) {
					once.Do(func() {
						activities.Delete(id)
						evidence.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
						evidence.DurationMs = time.Since(started).Milliseconds()
						evidence.Status, evidence.Detail = "completed", detail
						if failure != nil {
							evidence.Status = "failed"
						}
						if err := hcpo.savePythonStepEvidence(parent, evidence); err != nil {
							hcpo.GetLogger().Warn("Cannot save Python step completion: " + err.Error())
						}
						if notifier != nil {
							notifier.OnExecutionComplete(id, title, detail, metadata, failure)
						}
					})
				}
				if notifier != nil {
					notifier.OnExecutionStart(WorkshopExecutionStart{ID: id, ParentExecutionID: currentWorkshopParentExecutionID(ctx), Name: title, Kind: "workflow_step", Metadata: metadata})
				}
				activities.Store(id, finish)
				context.AfterFunc(parent, func() { finish("Python execution ended before this step completed.", parent.Err()) })
				call.Reply.OK, call.Reply.ConversationID = true, id
			} else if phase == "finish" {
				execution, _ := arguments["execution_id"].(string)
				done, found := activities.LoadAndDelete(execution)
				ok, valid := arguments["ok"].(bool)
				if !found || !valid {
					call.Reply.Error = "This script has no matching active step."
					break
				}
				detail, _ := arguments["error"].(string)
				detail = pythonHelperPreview(redaction.Text(detail))
				var failure error
				if !ok {
					failure = errors.New(detail)
				}
				done.(func(string, error))(detail, failure)
				call.Reply.OK = true
			} else {
				call.Reply.Error = "Choose a step start or completion."
			}

		case "check":
			name, _ := arguments["name"].(string)
			ok, valid := arguments["ok"].(bool)
			detail, _ := arguments["detail"].(string)
			if name == "" || !valid {
				call.Reply.Error = "A check needs its name and a True or False result."
				break
			}
			call.Reply.OK = ok
			name, detail = pythonHelperPreview(name), pythonHelperPreview(detail)
			if !ok {
				call.Reply.Error = detail
				if call.Reply.Error == "" {
					call.Reply.Error = "Check failed: " + name
				}
			}
			publish, notice, limitErr := hcpo.countPythonHelper(ctx, action)
			if limitErr != nil {
				call.Reply.OK, call.Reply.Error = false, limitErr.Error()
				break
			}
			call.Reply.Text = notice
			if notifier := hcpo.workshopExecutionNotifier; publish && notifier != nil {
				notifier.OnExecutionStart(WorkshopExecutionStart{ID: id, ParentExecutionID: currentWorkshopParentExecutionID(ctx), Name: name, Kind: "validation", Cancel: cancel,
					Metadata: map[string]string{"inspection_kind": "check", "log_file": log.Path()}})
				var failure error
				if !ok {
					failure = errors.New(call.Reply.Error)
				}
				notifier.OnExecutionComplete(id, name, scriptOutputPreview(detail, log.Path()), map[string]string{"suppress_auto_notification": "true", "log_file": log.Path()}, failure)
			}
		case "notify":
			text, _ := arguments["text"].(string)
			publish, notice, limitErr := hcpo.countPythonHelper(ctx, action)
			call.Reply.OK, call.Reply.Text = true, notice
			if limitErr != nil {
				call.Reply.OK, call.Reply.Error = false, limitErr.Error()
			} else if !publish {
				// Full input stays in this call's log; the chat receives no flood.
			} else if text == "" {
				call.Reply.OK, call.Reply.Error = false, "Notification text is empty."
			} else {
				var target ScriptNotificationTarget
				if opts := hcpo.executionOptions; opts != nil && opts.CrewResources != nil {
					target, _ = opts.CrewResources.Scripts.(ScriptNotificationTarget)
				}
				if target == nil {
					call.Reply.OK, call.Reply.Error = false, "Progress was saved to the log; no chat destination is available."
				} else if destination, err := target.NotifyScript(ctx, hcpo.GetWorkspacePath(), hcpo.httpSessionID, id, pythonHelperPreview(text)); err != nil || destination == "" {
					call.Reply.OK, call.Reply.Error = false, "Progress was saved to the log but could not be delivered to a chat."
					if err != nil {
						call.Reply.Error += " " + pythonHelperPreview(err.Error())
					}
				}
			}

		}
		// This activity event precedes closing the log. Final log evidence
		// belongs only in the flushed response and durable result receipt.
		resultJSON, _ := json.Marshal(map[string]interface{}{"ok": call.Reply.OK, "error": call.Reply.Error, "text": call.Reply.Text, "conversation_id": call.Reply.ConversationID, "value": call.Reply.Value})
		_, _ = log.Stream("result").Write(append(resultJSON, '\n'))
		log.Note(action + " finished")
		log.Close()
		if call.Reply.LogFile == "" || call.Reply.LogFile == log.Path() {
			call.Reply.LogFile, call.Reply.LogComplete, call.Reply.LogError = log.Path(), log.Error() == "", log.Error()
		} else if log.Error() != "" {
			call.Reply.LogError += " Helper activity log: " + log.Error()
		}
		if err := hcpo.savePythonRunEvidence(ctx, id, action, arguments, call.Reply); err != nil {
			call.Reply.LogError += " Run evidence could not be saved: " + err.Error()
		}
		call.Complete = true
		if err := persist(); err != nil {
			call.Reply.LogError = "Helper finished but its result receipt could not be saved: " + err.Error()
		}
		return encode(call.Reply)
	}
}

func (hcpo *StepBasedWorkflowOrchestrator) openPythonHelperLog(id string) (*runlog.Log, error) {
	if hcpo.selectedRunFolder == "" {
		return nil, fmt.Errorf("there is no active run folder")
	}
	relative := filepath.ToSlash(filepath.Join("runs", hcpo.selectedRunFolder, "logs", "helpers", id+".log"))
	root, name, err := workflowfiles.OpenWorkflowDirectory(GetPromptDocsRoot(), hcpo.GetWorkspacePath(), relative)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	parent, err := workflowfiles.OpenDirectory(root, filepath.ToSlash(filepath.Dir(name)), true)
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	if err := workflowfiles.ProtectServerTree(parent); err != nil {
		return nil, err
	}
	return runlog.CreateServerOwned(parent, filepath.Base(name))
}

func pythonBundleFingerprint(view fs.FS, directory string) (string, error) {
	hash := sha256.New()
	err := fs.WalkDir(view, directory, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == "__pycache__" {
				return fs.SkipDir
			}
			return nil
		}
		file, err := view.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()
		_, _ = io.WriteString(hash, strings.TrimPrefix(path, directory+"/")+"\x00")
		if _, err := io.Copy(hash, file); err != nil {
			return err
		}
		_, _ = hash.Write([]byte{0})
		return nil
	})
	return hex.EncodeToString(hash.Sum(nil)), err
}

// Limits apply to presentation only. Every actual call still gets its complete
// authorized activity log; Python, never a notification, decides the run result.
func pythonHelperPreview(text string) string {
	const max = 4096
	const marker = "\n[truncated; full text in helper log]"
	if len(text) <= max {
		return text
	}
	return strings.ToValidUTF8(text[:max-len(marker)], "") + marker
}

type pythonHelperLimits struct {
	Notifications int `json:"notifications"`
	Checks        int `json:"checks"`
}

func (hcpo *StepBasedWorkflowOrchestrator) countPythonHelper(ctx context.Context, action string) (bool, string, error) {
	hcpo.pythonHelperLimitsMu.Lock()
	defer hcpo.pythonHelperLimitsMu.Unlock()
	relative := filepath.ToSlash(filepath.Join("runs", hcpo.selectedRunFolder, "helpers", "presentation.json"))
	view := workflowfiles.WorkflowFS{Docs: GetPromptDocsRoot(), Root: hcpo.GetWorkspacePath()}
	var state pythonHelperLimits
	if data, err := fs.ReadFile(view, relative); err == nil {
		if err := json.Unmarshal(data, &state); err != nil {
			return false, "", fmt.Errorf("Cannot read this run's helper counters: %w", err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return false, "", err
	}
	count, limit := &state.Checks, 1000
	if action == "notify" {
		count, limit = &state.Notifications, 100
	}
	// Saturating at limit+2 records the once-only notice without overflow.
	if *count < limit+2 {
		*count++
	}
	data, _ := json.Marshal(state)
	if err := hcpo.writePythonHelperState(ctx, filepath.ToSlash(filepath.Join(hcpo.GetWorkspacePath(), relative)), string(data)); err != nil {
		return false, "", err
	}
	notice := ""
	if *count == limit+1 {
		notice = fmt.Sprintf("This run's %s display limit (%d) was reached. Further calls are saved in helper logs only.", action, limit)
	}
	return *count <= limit, notice, nil
}
