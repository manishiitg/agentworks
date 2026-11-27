package server

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Switch after this reply (owner, 27 Nov): a model picked while the chat is busy waits for the reply instead of
// failing. "Summarize & switch" was refused mid-reply ("This chat is busy") and the switch was dropped. The server
// now keeps the choice and carries it out once the reply, anything queued behind it and its background work end:
// it compacts first when the person picked that, waits for the compaction, then saves the model through the same
// chat-models handler and checks as a switch made by hand. The server waits, not the browser, so closing the tab
// does not lose it. A newer choice replaces an older one; Cancel drops it. Kept in memory: a server restart drops
// a pending switch, and the chat keeps its current model.

// chatModelSwitchRequest is the model to switch to, as the chat-models handler takes it.
type chatModelSwitchRequest struct {
	ProjectID       string `json:"project_id"`
	Engine          string `json:"engine"`
	ModelID         string `json:"model_id,omitempty"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
	ConnectionID    string `json:"connection_id,omitempty"`
	// Label is what the chip shows ("Sonnet 5.5"); display only.
	Label string `json:"label,omitempty"`
	// Compact summarizes the chat before switching ("Summarize & switch").
	Compact bool `json:"compact,omitempty"`
}

// chatModelSwitchView is the pending switch in the chat's context view, or its last failure.
type chatModelSwitchView struct {
	Label   string `json:"label"`
	Compact bool   `json:"compact,omitempty"`
	Stage   string `json:"stage"` // waiting, compacting, failed
	Error   string `json:"error,omitempty"`
}

type chatModelSwitchJob struct {
	request chatModelSwitchRequest
	stage   string
	err     string
	cancel  chan struct{}
}

var chatModelSwitches = struct {
	sync.Mutex
	bySession map[string]*chatModelSwitchJob
}{bySession: map[string]*chatModelSwitchJob{}}

// chatModelSwitchWait bounds how long a switch waits for a reply and for its compaction.
var chatModelSwitchWait = 60 * time.Minute

// chatModelSwitchPoll is how often a waiting switch checks the chat.
var chatModelSwitchPoll = 2 * time.Second

func chatModelSwitchViewFor(sessionID string) *chatModelSwitchView {
	chatModelSwitches.Lock()
	defer chatModelSwitches.Unlock()
	job := chatModelSwitches.bySession[sessionID]
	if job == nil {
		return nil
	}
	return &chatModelSwitchView{Label: job.request.Label, Compact: job.request.Compact, Stage: job.stage, Error: job.err}
}

// cancelChatModelSwitch drops the chat's pending switch (or clears a failure the screen has shown).
func cancelChatModelSwitch(sessionID string) bool {
	chatModelSwitches.Lock()
	defer chatModelSwitches.Unlock()
	job := chatModelSwitches.bySession[sessionID]
	if job == nil {
		return false
	}
	delete(chatModelSwitches.bySession, sessionID)
	if job.stage != "failed" {
		close(job.cancel)
	}
	return true
}

func (job *chatModelSwitchJob) setStage(sessionID, stage, err string) bool {
	chatModelSwitches.Lock()
	defer chatModelSwitches.Unlock()
	if chatModelSwitches.bySession[sessionID] != job {
		return false
	}
	job.stage, job.err = stage, err
	if stage == "" {
		delete(chatModelSwitches.bySession, sessionID)
	}
	return true
}

// queueChatModelSwitch saves a chat's pending switch, replacing (and stopping) an older one.
func queueChatModelSwitch(sessionID string, request chatModelSwitchRequest) *chatModelSwitchJob {
	job := &chatModelSwitchJob{request: request, stage: "waiting", cancel: make(chan struct{})}
	chatModelSwitches.Lock()
	defer chatModelSwitches.Unlock()
	if old := chatModelSwitches.bySession[sessionID]; old != nil && old.stage != "failed" {
		close(old.cancel)
	}
	chatModelSwitches.bySession[sessionID] = job
	return job
}

// switchModelAfterReply saves the switch and starts the wait. The caller is the chat's owner (chatContextTab).
func (api *StreamingAPI) switchModelAfterReply(ctx context.Context, owner string, tab chatContextTab, request chatModelSwitchRequest) (int, string) {
	request.ProjectID, request.Engine = strings.TrimSpace(request.ProjectID), strings.TrimSpace(request.Engine)
	if request.ProjectID == "" || request.Engine == "" {
		return http.StatusBadRequest, "Choose the model to switch to."
	}
	if strings.TrimSpace(request.Label) == "" {
		request.Label = firstNonEmptyTrimmed(request.ModelID, request.Engine)
	}
	sessionID := tab.Record.SessionID
	job := queueChatModelSwitch(sessionID, request)
	log.Printf("[MODEL_SWITCH] session=%s owner=%s engine=%s model=%s compact=%t action=queued busy=%t", sessionID, owner, request.Engine, request.ModelID, request.Compact, api.chatContextBusy(owner, sessionID) != "")
	go api.runModelSwitchAfterReply(context.WithoutCancel(ctx), owner, tab, job)
	return http.StatusAccepted, ""
}

// chatModelSwitchSteps are what a waiting switch does to the chat; the server's own in runModelSwitchAfterReply.
type chatModelSwitchSteps struct {
	busy       func() bool
	compact    func() (int, string)
	compacting func() bool
	save       func(chatModelSwitchRequest) (int, string)
}

func (api *StreamingAPI) runModelSwitchAfterReply(ctx context.Context, owner string, tab chatContextTab, job *chatModelSwitchJob) {
	sessionID := tab.Record.SessionID
	runChatModelSwitch(sessionID, owner, job, chatModelSwitchSteps{
		busy:       func() bool { return api.chatContextBusy(owner, sessionID) != "" },
		compact:    func() (int, string) { return api.compactChatNow(owner, tab, false) },
		compacting: func() bool { return idleCompacts.running(sessionID) != nil },
		save: func(req chatModelSwitchRequest) (int, string) {
			r := (&http.Request{}).WithContext(ctx)
			status, body := scheduleHandler(r, api.handleAgentProfileChatModels, http.MethodPost, "/api/agent-profiles/"+tab.ProfileID+"/chat-models", map[string]string{"id": tab.ProfileID}, nil, map[string]string{
				"project_id": req.ProjectID, "conversation_key": tab.Record.ConversationKey, "engine": req.Engine,
				"model_id": req.ModelID, "reasoning_effort": req.ReasoningEffort, "connection_id": req.ConnectionID,
			})
			var refusal struct {
				Error string `json:"error"`
			}
			_ = json.Unmarshal(body, &refusal)
			return status, refusal.Error
		},
	})
}

func runChatModelSwitch(sessionID, owner string, job *chatModelSwitchJob, steps chatModelSwitchSteps) {
	wait := func(done func() bool) bool {
		deadline := time.Now().Add(chatModelSwitchWait)
		for !done() {
			if time.Now().After(deadline) {
				return false
			}
			select {
			case <-job.cancel:
				return false
			case <-time.After(chatModelSwitchPoll):
			}
		}
		select {
		case <-job.cancel:
			return false
		default:
			return true
		}
	}
	cancelled := func() bool {
		select {
		case <-job.cancel:
			log.Printf("[MODEL_SWITCH] session=%s owner=%s action=cancelled", sessionID, owner)
			return true
		default:
			return false
		}
	}
	fail := func(reason string) {
		log.Printf("[MODEL_SWITCH] session=%s owner=%s action=failed reason=%q", sessionID, owner, reason)
		job.setStage(sessionID, "failed", reason)
	}
	if !wait(func() bool { return !steps.busy() }) {
		if !cancelled() {
			fail("The reply took too long, so the model was not changed. Pick it again.")
		}
		return
	}
	if job.request.Compact {
		if !job.setStage(sessionID, "compacting", "") {
			return
		}
		status, message := steps.compact()
		log.Printf("[MODEL_SWITCH] session=%s owner=%s action=compact status=%d message=%q", sessionID, owner, status, message)
		if status >= http.StatusBadRequest {
			fail("Could not summarize this chat (" + message + "), so the model was not changed.")
			return
		}
		if !wait(func() bool { return !steps.compacting() }) {
			if !cancelled() {
				fail("Summarizing took too long, so the model was not changed. Pick it again.")
			}
			return
		}
	}
	chatModelSwitches.Lock()
	current := chatModelSwitches.bySession[sessionID] == job
	chatModelSwitches.Unlock()
	if !current || cancelled() {
		return
	}
	req := job.request
	if status, refusal := steps.save(req); status >= http.StatusBadRequest {
		fail("Could not change the model: " + firstNonEmptyTrimmed(refusal, http.StatusText(status)) + ". This chat keeps its current model.")
		return
	}
	log.Printf("[MODEL_SWITCH] session=%s owner=%s engine=%s model=%s action=switched", sessionID, owner, req.Engine, req.ModelID)
	job.setStage(sessionID, "", "")
}
