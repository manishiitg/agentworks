// Package nativeinput maps native CLI questions onto the host's existing cards.
package nativeinput

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

type Option struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}
type Question struct {
	ID          string   `json:"id"`
	Header      string   `json:"header,omitempty"`
	Question    string   `json:"question"`
	Options     []Option `json:"options"`
	MultiSelect bool     `json:"multi_select,omitempty"`
	AllowOther  bool     `json:"allow_other,omitempty"`
}
type Answer struct {
	ID             string   `json:"id"`
	SelectedLabels []string `json:"selected_labels"`
	OtherText      string   `json:"other_text"`
}

// RequestKind is provider-supplied lifecycle information, never inferred from
// the question text or its option labels. Only ordinary questions may be
// recovered by the host as a later message after a lost native callback.
type RequestKind string

const (
	QuestionRequest        RequestKind = "question"
	CommandApprovalRequest RequestKind = "command_approval"
	PlanApprovalRequest    RequestKind = "plan_approval"
)

func Ask(ctx context.Context, questions []Question) ([]Answer, error) {
	return AskWithKind(ctx, QuestionRequest, questions)
}

func AskWithKind(ctx context.Context, kind RequestKind, questions []Question) ([]Answer, error) {
	b, err := json.Marshal(map[string]any{"questions": questions, "native_request_kind": kind})
	if err != nil {
		return nil, err
	}
	var args map[string]any
	_ = json.Unmarshal(b, &args)
	result, err := llmtypes.AskCodingAgentQuestion(ctx, args)
	if err != nil {
		return nil, err
	}
	var reply struct {
		Status  string   `json:"status"`
		Answers []Answer `json:"answers"`
	}
	if err = json.Unmarshal([]byte(result), &reply); err != nil {
		return nil, err
	}
	if reply.Status != "answered" || len(reply.Answers) != len(questions) {
		return nil, fmt.Errorf("question was not answered")
	}
	for i, a := range reply.Answers {
		q := questions[i]
		if a.ID != q.ID {
			return nil, fmt.Errorf("answer belongs to a different question")
		}
		seen := map[string]bool{}
		for _, label := range a.SelectedLabels {
			valid := false
			for _, o := range q.Options {
				if label == o.Label {
					valid = true
					break
				}
			}
			if !valid || seen[label] {
				return nil, fmt.Errorf("invalid question selection")
			}
			seen[label] = true
		}
		if a.OtherText != "" && !q.AllowOther {
			return nil, fmt.Errorf("custom answer is not supported")
		}
		n := len(Values(a))
		if n == 0 || (!q.MultiSelect && n != 1) {
			return nil, fmt.Errorf("invalid answer count")
		}
	}
	return reply.Answers, nil
}
func Values(a Answer) []string {
	v := append([]string{}, a.SelectedLabels...)
	if a.OtherText != "" {
		v = append(v, a.OtherText)
	}
	return v
}

// Requests lets the event loop continue observing native cancellation and exit
// while the host waits for a person. Completion never leaves a card pending.
type Requests struct {
	mu      sync.Mutex
	pending map[string]context.CancelFunc
}

func (r *Requests) Start(ctx context.Context, id string, run func(context.Context)) {
	r.mu.Lock()
	if r.pending == nil {
		r.pending = map[string]context.CancelFunc{}
	}
	if _, exists := r.pending[id]; exists {
		r.mu.Unlock()
		return
	}
	child, cancel := context.WithCancel(ctx)
	r.pending[id] = cancel
	r.mu.Unlock()
	go func() { defer cancel(); run(child); r.mu.Lock(); delete(r.pending, id); r.mu.Unlock() }()
}
func (r *Requests) Cancel(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if cancel := r.pending[id]; cancel != nil {
		cancel()
	}
}
func (r *Requests) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, cancel := range r.pending {
		cancel()
	}
}
func (r *Requests) Waiting() bool { r.mu.Lock(); defer r.mu.Unlock(); return len(r.pending) > 0 }
