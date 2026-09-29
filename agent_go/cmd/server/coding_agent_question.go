package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/musecli"
)

func (api *StreamingAPI) handleCodingAgentQuestionAnswer(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	sessionID := strings.TrimSpace(mux.Vars(r)["session_id"])
	if sessionID == "" || !api.canAccessTerminalSession(r, sessionID) {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}
	var req struct {
		Provider string `json:"provider"`
		PromptID string `json:"prompt_id"`
		// Auto answers with the first option of every question, the same
		// choice an unattended run makes.
		Auto    bool                             `json:"auto"`
		Answers []codingAgentQuestionAnswerInput `json:"answers"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)).Decode(&req); err != nil {
		http.Error(w, "Invalid question answer", http.StatusBadRequest)
		return
	}
	if req.PromptID == "" || (!req.Auto && len(req.Answers) == 0) || len(req.Answers) > 12 {
		http.Error(w, "Prompt and answers are required", http.StatusBadRequest)
		return
	}
	// Each checkbox toggle waits for the widget to redraw, so a multi-select
	// answer takes longer than a single choice.
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	provider := strings.TrimSpace(req.Provider)
	if provider == "" && strings.HasSuffix(r.URL.Path, "/muse-question/answer") {
		provider = "muse-cli"
	}
	if provider != "muse-cli" {
		http.Error(w, "Question choice delivery is not available for this provider", http.StatusNotImplemented)
		return
	}
	answers, err := museQuestionAnswers(sessionID, req.PromptID, req.Auto, req.Answers)
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	if err := musecli.SubmitQuestionAnswers(ctx, sessionID, req.PromptID, answers); err != nil {
		// "Let Muse choose" is the way out of a question; if even that cannot
		// be entered, interrupt the run rather than leave the chat waiting.
		if req.Auto && !strings.Contains(err.Error(), "no longer pending") {
			if interruptErr := musecli.InterruptPendingQuestion(ctx, sessionID); interruptErr == nil {
				http.Error(w, "Muse could not take its first option, so the run was stopped. Send your message again.", http.StatusConflict)
				return
			}
		}
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "prompt_id": req.PromptID})
}

type codingAgentQuestionAnswerInput = struct {
	ID             string   `json:"id"`
	SelectedLabels []string `json:"selected_labels"`
	SelectedLabel  string   `json:"selected_label"` // Legacy Muse client.
}

// museQuestionAnswers shapes the request for the prompt's own question kinds:
// one label for a single choice, a label list for a multi-select question.
// musecli validates the labels against the structured prompt.
func museQuestionAnswers(sessionID, promptID string, auto bool, input []codingAgentQuestionAnswerInput) ([]musecli.QuestionAnswer, error) {
	prompt, err := musecli.PendingQuestion(sessionID)
	if err != nil {
		return nil, err
	}
	if prompt == nil || prompt.PromptID != promptID {
		return nil, fmt.Errorf("Muse question is no longer pending")
	}
	if auto {
		return musecli.FirstOptionAnswers(prompt.Questions), nil
	}
	if len(input) != len(prompt.Questions) {
		return nil, fmt.Errorf("answer every question")
	}
	answers := make([]musecli.QuestionAnswer, 0, len(input))
	for i, answer := range input {
		labels := answer.SelectedLabels
		if len(labels) == 0 && answer.SelectedLabel != "" {
			labels = []string{answer.SelectedLabel}
		}
		if answer.ID == "" || len(labels) == 0 {
			return nil, fmt.Errorf("choose an option for every question")
		}
		if prompt.Questions[i].Multiple() {
			answers = append(answers, musecli.QuestionAnswer{ID: answer.ID, SelectedLabels: labels})
			continue
		}
		if len(labels) != 1 {
			return nil, fmt.Errorf("choose one option for %s", answer.ID)
		}
		answers = append(answers, musecli.QuestionAnswer{ID: answer.ID, SelectedLabel: labels[0]})
	}
	return answers, nil
}
